// Package mcpdaemon discovers terminal-owned sessions and routes MCP requests
// over authenticated local IPC.
package mcpdaemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const protocolVersion = 2

var ErrRunning = errors.New("xserial MCP daemon is already running")
var ErrIncompatible = errors.New("incompatible xserial MCP daemon; stop it before restarting with this executable")

type Options struct {
	StateDir string
	URL      string
	Stderr   io.Writer
	Demo     bool
	Version  string
}
type metadata struct {
	Protocol int    `json:"protocol"`
	Address  string `json:"address"`
	Token    string `json:"token"`
	PID      int    `json:"pid"`
	Demo     bool   `json:"demo"`
	Version  string `json:"version"`
	URL      string `json:"url,omitempty"`
}

func StateDirectory(path string, demo bool) (string, error) {
	if path == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		name := "mcp"
		if demo {
			name = "mcp-demo"
		}
		path = filepath.Join(cache, "xserial", name)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return "", err
	}
	if err := protectDirectory(path); err != nil {
		return "", err
	}
	return path, nil
}

// Never unlink lock files: replacing their inode could allow two live owners.
// The kernel releases the lock even after a crash; a PID file is not a lock.
func acquire(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func waitLock(ctx context.Context, path string) (*os.File, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		file, err := acquire(path)
		if !errors.Is(err, ErrRunning) {
			return file, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func writeMetadata(dir string, meta metadata) error {
	file, err := os.CreateTemp(dir, "endpoint-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := json.NewEncoder(file).Encode(meta); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	path := filepath.Join(dir, "endpoint.json")
	// Removal is protected by daemon.lock and supports atomic rename on Windows.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(file.Name(), path)
}

func randomToken() string {
	var data [32]byte
	_, _ = rand.Read(data[:])
	return hex.EncodeToString(data[:])
}

func Ensure(ctx context.Context, opts Options) (*Client, error) {
	dir, err := StateDirectory(opts.StateDir, opts.Demo)
	if err != nil {
		return nil, err
	}
	opts.StateDir = dir
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if client, err := Connect(ctx, opts); err == nil || errors.Is(err, ErrIncompatible) {
		return client, err
	}
	lock, err := waitLock(ctx, filepath.Join(dir, "startup.lock"))
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if client, err := Connect(ctx, opts); err == nil || errors.Is(err, ErrIncompatible) {
		return client, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{"mcp", "daemon", "--state-dir", dir}
	if opts.URL != "" {
		args = append(args, "--url", opts.URL)
	}
	if opts.Demo {
		args = append(args, "--demo")
	}
	cmd := exec.Command(executable, args...)
	detach(cmd)
	cmd.Stderr = opts.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("start MCP daemon: %w", err)
	}
	// The daemon intentionally outlives this bridge; it owns its own lifecycle.
	if err := cmd.Process.Release(); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := Connect(ctx, opts)
		if err == nil || errors.Is(err, ErrIncompatible) {
			if client != nil {
				client.Started = true
			}
			return client, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for MCP daemon: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
