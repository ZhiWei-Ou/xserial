package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/demo"
	"github.com/ZhiWei-Ou/xserial/internal/mcpdaemon"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"io"
)

// Exercise the real command/auto-start path using this test executable. Only
// explicitly created child processes inherit this marker; no hardware is used.
func TestMain(m *testing.M) {
	if os.Getenv("XSERIAL_MCP_TEST_PROCESS") == "1" && len(os.Args) > 2 && os.Args[1] == "test-terminal" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		debug := debugsession.New(debugsession.Connection{})
		publication, err := mcpdaemon.Publish(ctx, mcpdaemon.Options{StateDir: os.Args[2], Demo: true}, debug)
		if err == nil {
			ready := make(chan middleware.Endpoint, 1)
			err = middleware.New(middleware.Config{Port: demo.NewPort(), Debug: debug, Connection: middleware.ConnectionConfig{PortName: "child-terminal", BaudRate: 115200, DataBits: 8, Parity: "none", StopBits: "1"}, Frontend: mcpFrontend{ready}}).Run(ctx)
			err = errors.Join(err, publication.Close())
		}
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("XSERIAL_MCP_TEST_PROCESS") == "1" && len(os.Args) > 1 && os.Args[1] == "mcp" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		cmd := NewRootCommand()
		cmd.SetArgs(os.Args[1:])
		err := cmd.ExecuteContext(ctx)
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func connectBridge(ctx context.Context, dir string) (*mcp.ClientSession, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, "mcp", "--transport", "stdio", "--url", "http://127.0.0.1:0/mcp", "--demo", "--state-dir", dir)
	command.Env = append(os.Environ(), "XSERIAL_MCP_TEST_PROCESS=1")
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	defer stderr.Close()
	command.Stderr = stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "coding-agent", Version: "test"}, nil)
	return client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 2 * time.Second}, nil)
}

func callTool[T any](t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args any) T {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("%s failed: %+v", name, result.Content)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output T
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatal(err)
	}
	return output
}

type mcpFrontend struct{ ready chan middleware.Endpoint }

func (f mcpFrontend) Run(_ context.Context, e middleware.Endpoint) error {
	events := e.Events()
	f.ready <- e
	for range events {
	}
	return nil
}

func testTerminal(t *testing.T, dir string) (*debugsession.Session, middleware.Endpoint) {
	t.Helper()
	debug := debugsession.New(debugsession.Connection{})
	publication, err := mcpdaemon.Publish(context.Background(), mcpdaemon.Options{StateDir: dir, Demo: true}, debug)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan middleware.Endpoint, 1)
	done := make(chan error, 1)
	go func() {
		done <- middleware.New(middleware.Config{Port: demo.NewPort(), Debug: debug, Connection: middleware.ConnectionConfig{PortName: "demo", BaudRate: 115200, DataBits: 8, Parity: "none", StopBits: "1"}, Frontend: mcpFrontend{ready}}).Run(ctx)
	}()
	e := <-ready
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := publication.Close(); err != nil {
			t.Error(err)
		}
	})
	return debug, e
}

func waitBridgeSession(t *testing.T, ctx context.Context, session *mcp.ClientSession) debugsession.Status {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := callTool[debugsession.StatusResult](t, ctx, session, "serial_status", struct{}{})
		if len(status.Sessions) == 1 {
			return status.Sessions[0]
		}
		select {
		case <-ctx.Done():
			t.Fatal("terminal not attached")
		case <-ticker.C:
		}
	}
}

func TestMCPConcurrentStdioStartupAndSharedTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "state")
	_, terminal := testTerminal(t, dir)
	opts := mcpdaemon.Options{StateDir: dir, Demo: true}
	var workers sync.WaitGroup
	sessions := make([]*mcp.ClientSession, 3)
	errs := make([]error, 3)
	for i := range sessions {
		workers.Go(func() { sessions[i], errs[i] = connectBridge(ctx, dir) })
	}
	workers.Wait()
	t.Cleanup(func() {
		for _, session := range sessions {
			if session != nil {
				_ = session.Close()
			}
		}
		if err := mcpdaemon.Stop(context.Background(), opts); err != nil {
			t.Error(err)
		}
	})
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	client, err := mcpdaemon.Connect(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	pid := client.Meta.PID
	_ = client.Close()
	status := waitBridgeSession(t, ctx, sessions[0])
	sent := callTool[debugsession.SendResult](t, ctx, sessions[0], "serial_send", map[string]any{"session_id": status.SessionID, "data": "01 03 00 00 00 02 C4 0B", "encoding": "hex"})
	read := callTool[debugsession.ReadResult](t, ctx, sessions[0], "serial_read", map[string]any{"session_id": status.SessionID, "cursor": sent.Cursor, "idle_ms": 0})
	data, err := base64.StdEncoding.DecodeString(read.DataBase64)
	if err != nil || len(data) != 9 || data[4] != 100 {
		t.Fatalf("response=%x, %v", data, err)
	}
	observed := callTool[debugsession.ReadResult](t, ctx, sessions[1], "serial_read", map[string]any{"session_id": status.SessionID, "cursor": sent.Cursor, "wait_ms": 0})
	if observed.DataBase64 != read.DataBase64 {
		t.Fatal("observer consumed data")
	}
	callTool[debugsession.SendResult](t, ctx, sessions[1], "serial_send", map[string]any{"session_id": status.SessionID, "data": "x"})
	_ = sessions[0].Close()
	sessions[0] = nil
	if resumed := waitBridgeSession(t, ctx, sessions[1]); resumed.SessionID != status.SessionID {
		t.Fatal("client close changed serial identity")
	}
	client, err = mcpdaemon.Connect(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if client.Meta.PID != pid {
		t.Fatal("multiple daemon processes")
	}
	_ = client.Close()
	for i, session := range sessions {
		if session != nil {
			_ = session.Close()
			sessions[i] = nil
		}
	}
	if err := mcpdaemon.Stop(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if err := terminal.Send(ctx, []byte("human")); err != nil {
		t.Fatal("stop closed terminal:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "daemon.log")); !os.IsNotExist(err) {
		t.Fatalf("audit file=%v", err)
	}
}

func TestMCPDaemonCrashRetainsTerminalIdentityAndCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "state")
	debug, terminal := testTerminal(t, dir)
	opts := mcpdaemon.Options{StateDir: dir, Demo: true}
	session, err := connectBridge(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if session != nil {
			_ = session.Close()
		}
		if err := mcpdaemon.Stop(context.Background(), opts); err != nil {
			t.Error(err)
		}
	})
	status := waitBridgeSession(t, ctx, session)
	sent := callTool[debugsession.SendResult](t, ctx, session, "serial_send", map[string]any{"session_id": status.SessionID, "data": "01 03 00 00 00 02 C4 0B", "encoding": "hex"})
	before := callTool[debugsession.ReadResult](t, ctx, session, "serial_read", map[string]any{"session_id": status.SessionID, "cursor": sent.Cursor, "idle_ms": 0})
	client, err := mcpdaemon.Connect(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	process, err := os.FindProcess(client.Meta.PID)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = process.Release()
	if err := mcpdaemon.Stop(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if err := terminal.Send(ctx, []byte("human")); err != nil {
		t.Fatal(err)
	}
	if debug.Status().SessionID != status.SessionID {
		t.Fatal("daemon crash changed identity")
	}
	_ = session.Close()
	session = nil
	session, err = connectBridge(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	afterStatus := waitBridgeSession(t, ctx, session)
	if afterStatus.SessionID != status.SessionID {
		t.Fatal("restart did not reattach same session")
	}
	after := callTool[debugsession.ReadResult](t, ctx, session, "serial_read", map[string]any{"session_id": status.SessionID, "cursor": sent.Cursor, "wait_ms": 0})
	beforeData, _ := base64.StdEncoding.DecodeString(before.DataBase64)
	afterData, _ := base64.StdEncoding.DecodeString(after.DataBase64)
	if !bytes.HasPrefix(afterData, beforeData) {
		t.Fatalf("history changed: %+v, %+v", before, after)
	}
}

func TestMCPCommandRejectsInvalidTransportAndNonlocalURL(t *testing.T) {
	for _, args := range [][]string{{"mcp", "--transport", "bad"}, {"mcp", "--url", "http://example.com/mcp"}} {
		cmd := NewRootCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(context.Background()); err == nil {
			t.Fatal("accepted", args)
		}
	}
}

func TestAbnormalTerminalExitDetachesWithoutTrustingStalePID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "state")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command(executable, "test-terminal", dir)
	process.Env = append(os.Environ(), "XSERIAL_MCP_TEST_PROCESS=1")
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	process.Stderr = stderr
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stop := func() { once.Do(func() { _ = process.Process.Kill(); _ = process.Wait() }) }
	t.Cleanup(stop)
	bridge, err := connectBridge(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	opts := mcpdaemon.Options{StateDir: dir, Demo: true}
	t.Cleanup(func() {
		if err := mcpdaemon.Stop(context.Background(), opts); err != nil {
			t.Error(err)
		}
	})
	status := waitBridgeSession(t, ctx, bridge)
	stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		current := callTool[debugsession.StatusResult](t, ctx, bridge, "serial_status", struct{}{})
		if len(current.Sessions) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("crashed terminal stayed attached")
		case <-ticker.C:
		}
	}
	paths, err := filepath.Glob(filepath.Join(dir, "sessions", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("expected stale descriptor, %v %v", paths, err)
	}
	result, err := bridge.CallTool(ctx, &mcp.CallToolParams{Name: "serial_send", Arguments: map[string]any{"session_id": status.SessionID, "data": "old"}})
	if err != nil || !result.IsError {
		t.Fatalf("stale request=%+v, %v", result, err)
	}
}

func TestReusedStdioBridgePrintsAuditOnlyToStderr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "state")
	_, _ = testTerminal(t, dir)
	starter, err := connectBridge(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer starter.Close()
	t.Cleanup(func() {
		if err := mcpdaemon.Stop(context.Background(), mcpdaemon.Options{StateDir: dir, Demo: true}); err != nil {
			t.Error(err)
		}
	})
	status := waitBridgeSession(t, ctx, starter)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	capture, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	command := exec.Command(executable, "mcp", "--transport", "stdio", "--demo", "--state-dir", dir)
	command.Env = append(os.Environ(), "XSERIAL_MCP_TEST_PROCESS=1")
	command.Stderr = capture
	session, err := mcp.NewClient(&mcp.Implementation{Name: "audit-observer", Version: "test"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	callTool[debugsession.SendResult](t, ctx, session, "serial_send", map[string]any{"session_id": status.SessionID, "data": "audit-probe\n"})
	// The subscription is asynchronous. Wait for its observable stderr record,
	// not a guessed scheduling delay; successful MCP decoding protects stdout.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(capture.Name())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("audit-probe")) && bytes.Contains(data, []byte("[ INFO | mcp.send ]")) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("stdio audit missing: %s", data)
		case <-ticker.C:
		}
	}
}
