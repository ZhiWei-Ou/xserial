package mcpdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/demo"
	"github.com/ZhiWei-Ou/xserial/internal/serialport"
)

func testDeps() debugsession.Dependencies {
	return debugsession.Dependencies{
		List: func() ([]serialport.Info, error) { return []serialport.Info{{Name: "demo"}}, nil },
		Open: func(context.Context, debugsession.Connection) (io.ReadWriteCloser, error) { return demo.NewPort(), nil },
	}
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

func startTestDaemon(t *testing.T) (Options, *Client, <-chan error) {
	t.Helper()
	opts := Options{StateDir: filepath.Join(t.TempDir(), "state"), Version: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts, testDeps()) }()
	t.Cleanup(func() {
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
	return opts, connectWhenReady(t, opts), done
}

func TestDaemonIsUniqueAndOwnsPersistentSession(t *testing.T) {
	opts, first, _ := startTestDaemon(t)
	if err := Run(context.Background(), opts, testDeps()); !errors.Is(err, ErrRunning) {
		t.Fatalf("duplicate daemon = %v", err)
	}
	second := connectWhenReady(t, opts)
	ctx := context.Background()
	var opened debugsession.OpenResult
	if err := first.Call(ctx, "open", debugsession.Connection{Port: "demo"}, &opened); err != nil {
		t.Fatal(err)
	}
	var rejected debugsession.OpenResult
	if err := second.Call(ctx, "open", debugsession.Connection{Port: "demo"}, &rejected); err == nil {
		t.Fatal("second controller was accepted")
	}
	var sent debugsession.SendResult
	if err := first.Call(ctx, "send", debugsession.SendInput{SessionID: opened.SessionID, Data: "01 03 00 00 00 02 C4 0B", Encoding: "hex"}, &sent); err != nil {
		t.Fatal(err)
	}
	zero := 0
	var received debugsession.ReadResult
	if err := second.Call(ctx, "read", debugsession.ReadInput{SessionID: opened.SessionID, Cursor: sent.Cursor, IdleMS: &zero}, &received); err != nil {
		t.Fatal(err)
	}
	if received.Bytes != 9 {
		t.Fatalf("read = %+v", received)
	}
	_ = first.Close()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		var status debugsession.StatusResult
		if err := second.Call(ctx, "status", struct{}{}, &status); err != nil {
			t.Fatal(err)
		}
		if status.Control == "available" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("controller was not released")
		case <-ticker.C:
		}
	}
	var reused debugsession.OpenResult
	if err := second.Call(ctx, "open", debugsession.Connection{Port: "demo"}, &reused); err != nil || !reused.Reused || reused.SessionID != opened.SessionID {
		t.Fatalf("reopen = %+v, %v", reused, err)
	}
}

func TestCancelReadKeepsDaemonConnectionAndController(t *testing.T) {
	_, client, _ := startTestDaemon(t)
	ctx := context.Background()
	var opened debugsession.OpenResult
	if err := client.Call(ctx, "open", debugsession.Connection{Port: "demo"}, &opened); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithCancel(ctx)
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		var result debugsession.ReadResult
		wait := 30000
		done <- client.Call(readCtx, "read", debugsession.ReadInput{SessionID: opened.SessionID, Cursor: opened.Cursor, WaitMS: &wait}, &result)
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	var status debugsession.StatusResult
	if err := client.Call(ctx, "status", struct{}{}, &status); err != nil || status.Control != "owned" || status.State != "connected" {
		t.Fatalf("status = %+v, %v", status, err)
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
		t.Fatalf("reply = %+v, %v", reply, err)
	}
	if err := p.Cancel(1, &canceled); err != nil {
		t.Fatal(err)
	}
	if len(p.canceled) != 0 || len(p.finished) != 0 {
		t.Fatal("completed cancellation retained state")
	}
}

func TestDaemonIgnoresStalePIDMetadataAndRejectsUnauthenticatedPeer(t *testing.T) {
	dir, err := StateDirectory(filepath.Join(t.TempDir(), "state"), false)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{StateDir: dir}
	data, _ := json.Marshal(metadata{Protocol: 1, Address: "127.0.0.1:1", Token: randomToken(), PID: os.Getpid()})
	if err := os.WriteFile(filepath.Join(opts.StateDir, "endpoint.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts, testDeps()) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	client := connectWhenReady(t, opts)
	conn, err := net.Dial("tcp", client.Meta.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(conn, "0000000000000000000000000000000000000000000000000000000000000000"); err != nil {
		t.Fatal(err)
	}
	var reply [1]byte
	if _, err := conn.Read(reply[:]); err == nil {
		t.Fatal("unauthenticated peer accepted")
	}
	var status debugsession.StatusResult
	if err := client.Call(context.Background(), "status", struct{}{}, &status); err != nil {
		t.Fatal(err)
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
