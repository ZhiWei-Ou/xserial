package mcpdaemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/demo"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type safeBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *safeBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}
func (b *safeBuffer) text() string { b.mu.Lock(); defer b.mu.Unlock(); return b.Buffer.String() }

type testFrontend struct{ ready chan middleware.Endpoint }

func (f testFrontend) Run(ctx context.Context, e middleware.Endpoint) error {
	events := e.Events()
	f.ready <- e
	for range events {
	}
	return nil
}

type terminalFixture struct {
	session     *debugsession.Session
	endpoint    middleware.Endpoint
	publication *Publication
	stop        func()
}

func startTerminal(t *testing.T, opts Options, name string) terminalFixture {
	t.Helper()
	debug := debugsession.New(debugsession.Connection{})
	publication, err := Publish(context.Background(), opts, debug)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan middleware.Endpoint, 1)
	done := make(chan error, 1)
	go func() {
		done <- middleware.New(middleware.Config{Port: demo.NewPort(), Connection: middleware.ConnectionConfig{PortName: name, BaudRate: 115200, DataBits: 8, Parity: "none", StopBits: "1"}, Debug: debug, Frontend: testFrontend{ready}}).Run(ctx)
	}()
	endpoint := <-ready
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
			if err := publication.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return terminalFixture{debug, endpoint, publication, stop}
}

func connectWhenReady(t *testing.T, opts Options) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		client, err := Connect(ctx, opts)
		if err == nil {
			t.Cleanup(func() { _ = client.Close() })
			return client
		}
		select {
		case <-ctx.Done():
			t.Fatalf("daemon did not become ready: %v", err)
		case <-ticker.C:
		}
	}
}

func startTestDaemon(t *testing.T, opts Options) (*Client, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(3 * time.Second):
				t.Error("daemon did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return connectWhenReady(t, opts), stop
}

func attached(t *testing.T, c *Client, count int) []debugsession.Status {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result debugsession.StatusResult
		if err := c.Call(ctx, "status", struct{}{}, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) == count {
			return result.Sessions
		}
		select {
		case <-ctx.Done():
			t.Fatalf("attached = %+v, want %d", result, count)
		case <-ticker.C:
		}
	}
}
func testOptions(t *testing.T) Options {
	return Options{StateDir: filepath.Join(t.TempDir(), "state"), URL: "http://127.0.0.1:0/custom/mcp", Version: "test", Stderr: io.Discard}
}

func TestDiscoveryBothStartupOrdersMultipleTerminalsAndRestart(t *testing.T) {
	for _, terminalFirst := range []bool{true, false} {
		t.Run(fmtBool(terminalFirst), func(t *testing.T) {
			opts := testOptions(t)
			var terminal terminalFixture
			if terminalFirst {
				terminal = startTerminal(t, opts, "one")
			}
			client, stop := startTestDaemon(t, opts)
			if !terminalFirst {
				attached(t, client, 0)
				if err := client.Call(context.Background(), "send", debugsession.SendInput{SessionID: "missing", Data: "x"}, nil); err == nil {
					t.Fatal("missing session accepted")
				}
				terminal = startTerminal(t, opts, "one")
			}
			startTerminal(t, opts, "two")
			status := attached(t, client, 2)
			if status[0].Connection.Port == status[1].Connection.Port {
				t.Fatal("multiple ports not exposed")
			}
			if err := Run(context.Background(), opts); !errors.Is(err, ErrRunning) || !strings.Contains(err.Error(), client.Meta.URL) {
				t.Fatalf("duplicate = %v", err)
			}
			id := terminal.session.Status().SessionID
			var sent debugsession.SendResult
			in := debugsession.SendInput{SessionID: id, Data: "01 03 00 00 00 02 C4 0B", Encoding: "hex"}
			if err := client.Call(context.Background(), "send", in, &sent); err != nil {
				t.Fatal(err)
			}
			zero := 0
			readIn := debugsession.ReadInput{SessionID: id, Cursor: sent.Cursor, IdleMS: &zero}
			var first, second debugsession.ReadResult
			if err := client.Call(context.Background(), "read", readIn, &first); err != nil || first.Bytes != 9 {
				t.Fatalf("read = %+v, %v", first, err)
			}
			observer := connectWhenReady(t, opts)
			if err := observer.Call(context.Background(), "read", readIn, &second); err != nil || first != second {
				t.Fatalf("observer = %+v, %v", second, err)
			}
			if err := observer.Call(context.Background(), "send", in, &sent); err != nil {
				t.Fatal("second client cannot send:", err)
			}
			stop()
			if err := terminal.endpoint.Send(context.Background(), []byte("human")); err != nil {
				t.Fatal("daemon stop affected terminal:", err)
			}
			client, _ = startTestDaemon(t, opts)
			attached(t, client, 2)
			if terminal.session.Status().SessionID != id {
				t.Fatal("daemon restart changed terminal identity")
			}
			readIn.WaitMS = &zero
			if err := client.Call(context.Background(), "read", readIn, &second); err != nil || second.Bytes < first.Bytes {
				t.Fatalf("history lost: %+v, %v", second, err)
			}
			terminal.stop()
			attached(t, client, 1)
		})
	}
}
func fmtBool(value bool) string {
	if value {
		return "terminal_first"
	}
	return "daemon_first"
}

func TestHTTPClientsCustomURLAuditAndStopPreserveTerminal(t *testing.T) {
	opts := testOptions(t)
	logs := &safeBuffer{}
	opts.Stderr = logs
	terminal := startTerminal(t, opts, "demo")
	client, stop := startTestDaemon(t, opts)
	attached(t, client, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connect := func() *mcp.ClientSession {
		session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: client.Meta.URL}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		return session
	}
	first, second := connect(), connect()
	tools, err := first.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) != 3 {
		t.Fatalf("tools=%+v, %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != "serial_status" && tool.Name != "serial_read" && tool.Name != "serial_send" {
			t.Fatal(tool.Name)
		}
	}
	id := terminal.session.Status().SessionID
	result, err := first.CallTool(ctx, &mcp.CallToolParams{Name: "serial_send", Arguments: map[string]any{"session_id": id, "data": strings.Repeat("x", 256) + "UNLOGGED_SUFFIX\x1b\n"}})
	if err != nil || result.IsError {
		t.Fatalf("send=%+v, %v", result, err)
	}
	terminal.session.Received(1, []byte("DEVICE_SECRET_OUTPUT"))
	_, err = second.CallTool(ctx, &mcp.CallToolParams{Name: "serial_read", Arguments: map[string]any{"session_id": id, "wait_ms": 0}})
	if err != nil {
		t.Fatal(err)
	}
	rejected, rejectErr := first.CallTool(ctx, &mcp.CallToolParams{Name: "serial_send", Arguments: map[string]any{"session_id": id}})
	if rejectErr == nil && !rejected.IsError {
		t.Fatal("schema validation accepted missing data")
	}
	text := logs.text()
	if !strings.Contains(text, "invalid tool arguments or unknown tool") {
		t.Fatalf("schema rejection was not audited: %s", text)
	}
	for _, want := range []string{"[ INFO | mcp.started ]", client.Meta.URL, "mcp.client_connected", "mcp.attach", "mcp.send", "mcp.read", "truncated=true", "client_id=", "request_id=", "session_id=", "duration_ms=", "result=\"completed\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	for _, secret := range []string{client.Meta.Token, "DEVICE_SECRET_OUTPUT", "UNLOGGED_SUFFIX"} {
		if strings.Contains(text, secret) {
			t.Fatal("audit leaked", secret)
		}
	}
	response, err := http.Get(strings.TrimSuffix(client.Meta.URL, "/custom/mcp") + "/wrong")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal(response.StatusCode)
	}
	if err := Stop(ctx, opts); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := terminal.endpoint.Send(ctx, []byte("human")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(opts.StateDir, "daemon.log")); !os.IsNotExist(err) {
		t.Fatalf("audit file created: %v", err)
	}
}

func TestCanceledReadAndClosedPublicationKeepTerminalAlive(t *testing.T) {
	opts := testOptions(t)
	terminal := startTerminal(t, opts, "demo")
	client, _ := startTestDaemon(t, opts)
	attached(t, client, 1)
	status := terminal.session.Status()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Call(ctx, "read", debugsession.ReadInput{SessionID: status.SessionID, Cursor: "now"}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var sent debugsession.SendResult
	if err := client.Call(context.Background(), "send", debugsession.SendInput{SessionID: status.SessionID, Data: "x"}, &sent); err != nil {
		t.Fatal(err)
	}
	if err := terminal.publication.Close(); err != nil {
		t.Fatal(err)
	}
	attached(t, client, 0)
	if err := terminal.endpoint.Send(context.Background(), []byte("human")); err != nil {
		t.Fatal(err)
	}
}

func TestCancelArrivingBeforeDispatchIsNotLost(t *testing.T) {
	p := &peer{ctx: context.Background(), active: map[uint64]context.CancelFunc{}, canceled: map[uint64]bool{}, finished: map[uint64]bool{}}
	var canceled bool
	if err := p.Cancel(1, &canceled); err != nil || !canceled {
		t.Fatal("early cancel failed")
	}
	var reply Reply
	if err := p.Call(Request{ID: 1, Operation: "status"}, &reply); err != nil || reply.Fault == nil || reply.Fault.Code != "canceled" {
		t.Fatalf("reply=%+v, %v", reply, err)
	}
	_ = p.Cancel(1, &canceled)
	if len(p.canceled) != 0 || len(p.finished) != 0 {
		t.Fatal("retained cancellation")
	}
}

func TestAuthenticationProtocolCompatibilityAndStaleMetadata(t *testing.T) {
	opts := testOptions(t)
	dir, err := StateDirectory(opts.StateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := json.Marshal(metadata{Protocol: 1, Address: "127.0.0.1:1", Token: randomToken(), PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(dir, "endpoint.json"), stale, 0600); err != nil {
		t.Fatal(err)
	}
	client, _ := startTestDaemon(t, opts)
	conn, err := net.Dial("tcp", client.Meta.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = io.WriteString(conn, strings.Repeat("0", 64))
	var ack [1]byte
	if _, err := conn.Read(ack[:]); err == nil {
		t.Fatal("unauthenticated peer accepted")
	}
	meta := client.Meta
	meta.Protocol--
	if err := writeMetadata(dir, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := Connect(context.Background(), opts); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("compatibility=%v", err)
	}
	if _, err := Ensure(context.Background(), opts); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("autostart replaced old daemon: %v", err)
	}
}

func TestURLRejectsNonlocalAndMalformedEndpoints(t *testing.T) {
	for _, value := range []string{"https://127.0.0.1:8765/mcp", "http://0.0.0.0:8765/mcp", "http://example.com/mcp", "http://u:p@localhost/mcp", "http://localhost:70000/mcp", "http://localhost/mcp?q=1"} {
		if _, err := ParseURL(value); err == nil {
			t.Fatal("accepted", value)
		}
	}
	for _, value := range []string{"", "http://localhost:8766/agent", "http://[::1]:8765/mcp"} {
		if _, err := ParseURL(value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLockConcurrentAcquisitionAndRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	var acquired int
	var mu sync.Mutex
	var workers sync.WaitGroup
	files := make(chan *os.File, 8)
	for range 8 {
		workers.Go(func() {
			file, err := acquire(path)
			if err == nil {
				mu.Lock()
				acquired++
				mu.Unlock()
				files <- file
			} else if !errors.Is(err, ErrRunning) {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	close(files)
	if acquired != 1 {
		t.Fatalf("lock owners = %d", acquired)
	}
	for file := range files {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	file, err := acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatal("lock inode was removed")
	}
}
