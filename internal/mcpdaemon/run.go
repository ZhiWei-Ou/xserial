package mcpdaemon

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Run discovers terminal sessions and serves authenticated local IPC.
func Run(parent context.Context, opts Options) error {
	dir, err := StateDirectory(opts.StateDir)
	if err != nil {
		return err
	}
	lock, err := acquire(filepath.Join(dir, "daemon.lock"))
	if err != nil {
		if errors.Is(err, ErrRunning) {
			probe, cancel := context.WithTimeout(parent, time.Second)
			defer cancel()
			client, connectErr := Connect(probe, Options{StateDir: dir})
			if connectErr != nil {
				return errors.Join(err, connectErr)
			}
			defer client.Close()
			return ErrRunning
		}
		return err
	}
	defer lock.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	output := opts.Stderr
	if output == nil {
		output = os.Stderr
	}
	audit := newAudit(output)
	audit.event("mcp.started", "result", "completed")
	route := newRouter(dir, audit)
	route.ctx = ctx
	meta := metadata{Protocol: protocolVersion, Address: listener.Addr().String(), Token: randomToken(), PID: os.Getpid(), Version: opts.Version}
	if err := writeMetadata(dir, meta); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(dir, "endpoint.json"))
	discoveryDone := make(chan struct{})
	go func() { defer close(discoveryDone); route.run(ctx) }()
	runErr := serveRPC(ctx, listener, meta.Token, route, cancel, true)
	cancel()
	<-discoveryDone
	audit.event("mcp.stopped", "result", "completed")
	return runErr
}
