package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"github.com/ZhiWei-Ou/xserial/internal/mcpdaemon"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercise the real command/auto-start path using this test executable. Only
// explicitly created child processes inherit this marker; no hardware is used.
func TestMain(m *testing.M) {
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
	command := exec.Command(executable, "mcp", "--demo", "--state-dir", dir)
	command.Env = append(os.Environ(), "XSERIAL_MCP_TEST_PROCESS=1")
	command.Stderr = os.Stderr
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

func TestMCPConcurrentStartupDebuggingAndClientResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "state")
	opts := mcpdaemon.Options{StateDir: dir, Demo: true}
	var workers sync.WaitGroup
	sessions := make([]*mcp.ClientSession, 3)
	errors := make([]error, len(sessions))
	for i := range sessions {
		workers.Go(func() { sessions[i], errors[i] = connectBridge(ctx, dir) })
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
	for _, err := range errors {
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
	ports := callTool[debugsession.ListResult](t, ctx, sessions[0], "serial_list", struct{}{})
	if len(ports.Ports) != 1 || ports.Ports[0].Name != "demo" {
		t.Fatalf("ports = %+v", ports)
	}
	opened := callTool[debugsession.OpenResult](t, ctx, sessions[0], "serial_open", map[string]any{"port": "demo"})
	sent := callTool[debugsession.SendResult](t, ctx, sessions[0], "serial_send", map[string]any{"session_id": opened.SessionID, "data": "01 03 00 00 00 02 C4 0B", "encoding": "hex"})
	read := callTool[debugsession.ReadResult](t, ctx, sessions[0], "serial_read", map[string]any{"session_id": opened.SessionID, "cursor": sent.Cursor, "idle_ms": 5})
	data, err := base64.StdEncoding.DecodeString(read.DataBase64)
	if err != nil || len(data) != 9 || data[3] != 0 || data[4] != 100 {
		t.Fatalf("response = %x, %v", data, err)
	}
	observer := callTool[debugsession.ReadResult](t, ctx, sessions[1], "serial_read", map[string]any{"session_id": opened.SessionID, "cursor": sent.Cursor, "wait_ms": 0})
	if observer.DataBase64 != read.DataBase64 {
		t.Fatal("observers consumed different bytes")
	}
	rejected, err := sessions[1].CallTool(ctx, &mcp.CallToolParams{Name: "serial_open", Arguments: map[string]any{"port": "demo"}})
	if err != nil || !rejected.IsError {
		t.Fatalf("second controller = %+v, %v", rejected, err)
	}
	if err := sessions[0].Close(); err != nil {
		t.Fatal(err)
	}
	sessions[0] = nil
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		status := callTool[debugsession.StatusResult](t, ctx, sessions[1], "serial_status", struct{}{})
		if status.Control == "available" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("old controller was not released")
		case <-ticker.C:
		}
	}
	resumed := callTool[debugsession.OpenResult](t, ctx, sessions[1], "serial_open", map[string]any{"port": "demo"})
	if !resumed.Reused || resumed.SessionID != opened.SessionID {
		t.Fatalf("resume = %+v", resumed)
	}
	client, err = mcpdaemon.Connect(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if client.Meta.PID != pid {
		t.Fatalf("daemon changed from %d to %d", pid, client.Meta.PID)
	}
	_ = client.Close()
	callTool[debugsession.CloseResult](t, ctx, sessions[1], "serial_close", map[string]any{"session_id": opened.SessionID})
	callTool[debugsession.CloseResult](t, ctx, sessions[1], "serial_close", map[string]any{"session_id": opened.SessionID})
	for i, session := range sessions {
		if session != nil {
			_ = session.Close()
			sessions[i] = nil
		}
	}
	if err := mcpdaemon.Stop(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "endpoint.json")); !os.IsNotExist(err) {
		t.Fatalf("daemon endpoint remains after stop: %v", err)
	}
}

func TestMCPDaemonCrashRecoveryInvalidatesOldCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dir := filepath.Join(t.TempDir(), "state")
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
	opened := callTool[debugsession.OpenResult](t, ctx, session, "serial_open", map[string]any{"port": "demo"})
	client, err := mcpdaemon.Connect(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	pid := client.Meta.PID
	_ = client.Close()
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = process.Release()
	// Stop waits on the kernel lock, proving process death while preserving the
	// crash's stale endpoint file. It never signals a PID read from metadata.
	if err := mcpdaemon.Stop(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "endpoint.json")); err != nil {
		t.Fatalf("expected stale crash metadata: %v", err)
	}
	_ = session.Close()
	session = nil
	session, err = connectBridge(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	reopened := callTool[debugsession.OpenResult](t, ctx, session, "serial_open", map[string]any{"port": "demo"})
	if reopened.SessionID == opened.SessionID {
		t.Fatal("crash recovery reused an obsolete session ID")
	}
	rejected, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "serial_read", Arguments: map[string]any{"session_id": reopened.SessionID, "cursor": opened.Cursor, "wait_ms": 0}})
	if err != nil || !rejected.IsError {
		t.Fatalf("old cursor = %+v, %v", rejected, err)
	}
}
