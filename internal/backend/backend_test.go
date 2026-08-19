package backend

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type fakePort struct {
	reads  chan []byte
	closed chan struct{}
	once   sync.Once
	mu     sync.Mutex
	writes []byte
}

func newFakePort() *fakePort {
	return &fakePort{reads: make(chan []byte, 4), closed: make(chan struct{})}
}
func (p *fakePort) Read(dst []byte) (int, error) {
	select {
	case data := <-p.reads:
		return copy(dst, data), nil
	case <-p.closed:
		return 0, io.EOF
	}
}
func (p *fakePort) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes = append(p.writes, data...)
	return len(data), nil
}
func (p *fakePort) Close() error { p.once.Do(func() { close(p.closed) }); return nil }

type pollingPort struct {
	readStarted chan struct{}
	once        sync.Once
}

func (p *pollingPort) Read([]byte) (int, error) {
	p.once.Do(func() { close(p.readStarted) })
	time.Sleep(10 * time.Millisecond)
	return 0, nil
}

func (*pollingPort) Write(data []byte) (int, error) { return len(data), nil }
func (*pollingPort) Close() error                   { return nil }

func TestSessionTransfersBytes(t *testing.T) {
	port := newFakePort()
	ready := make(chan *Endpoint, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(Config{Port: port}).Run(ctx, ready) }()
	e := <-ready
	port.reads <- []byte{1, 2}
	event := <-e.Events()
	got := event.(Received).Data
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("received=%v", got)
	}
	if err := e.Send(ctx, []byte{3, 4}); err != nil {
		t.Fatal(err)
	}
	port.mu.Lock()
	writes := append([]byte(nil), port.writes...)
	port.mu.Unlock()
	if len(writes) != 2 || writes[0] != 3 || writes[1] != 4 {
		t.Fatalf("writes=%v", writes)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionCancellationDoesNotRequireCloseToUnblockRead(t *testing.T) {
	port := &pollingPort{readStarted: make(chan struct{})}
	ready := make(chan *Endpoint, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New(Config{Port: port}).Run(ctx, ready) }()
	<-ready
	<-port.readStarted

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("session remained blocked after cancellation")
	}
}

func TestSessionStopsAfterReconnectLimit(t *testing.T) {
	port := newFakePort()
	ready := make(chan *Endpoint, 1)
	attempts := 0
	missingErr := errors.New("missing")
	done := make(chan error, 1)
	go func() {
		done <- New(Config{Port: port, ReconnectAttempts: 2, ReconnectInterval: time.Millisecond, Reconnect: func() (Port, error) { attempts++; return nil, missingErr }}).Run(context.Background(), ready)
	}()
	e := <-ready
	_ = port.Close()
	var reconnecting int
	for event := range e.Events() {
		if _, ok := event.(Reconnecting); ok {
			reconnecting++
		}
	}
	err := <-done
	if !errors.Is(err, ErrReconnectExhausted) {
		t.Fatalf("error=%v", err)
	}
	if !errors.Is(err, missingErr) {
		t.Fatalf("error=%v does not wrap the last reconnect error", err)
	}
	if attempts != 2 || reconnecting != 2 {
		t.Fatalf("attempts=%d events=%d", attempts, reconnecting)
	}
}

func TestSessionReconfiguresActivePort(t *testing.T) {
	first := newFakePort()
	second := newFakePort()
	ready := make(chan *Endpoint, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- New(Config{Port: first, Reconnect: func() (Port, error) { return newFakePort(), nil }}).Run(ctx, ready)
	}()
	e := <-ready

	if err := e.Reconfigure(ctx, func() (Port, error) { return second, nil }); err != nil {
		t.Fatalf("Reconfigure() error = %v", err)
	}
	select {
	case <-first.closed:
	default:
		t.Fatal("old serial port remains open")
	}
	if err := e.Send(ctx, []byte("new port")); err != nil {
		t.Fatal(err)
	}
	second.mu.Lock()
	writes := string(second.writes)
	second.mu.Unlock()
	if writes != "new port" {
		t.Fatalf("replacement writes = %q", writes)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionRestoresPreviousConfigurationAfterReconfigureFailure(t *testing.T) {
	first := newFakePort()
	restored := newFakePort()
	ready := make(chan *Endpoint, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- New(Config{Port: first, Reconnect: func() (Port, error) { return restored, nil }}).Run(ctx, ready)
	}()
	e := <-ready
	want := errors.New("unsupported baud")

	if err := e.Reconfigure(ctx, func() (Port, error) { return nil, want }); !errors.Is(err, want) {
		t.Fatalf("Reconfigure() error = %v, want %v", err, want)
	}
	if err := e.Send(ctx, []byte("restored")); err != nil {
		t.Fatal(err)
	}
	restored.mu.Lock()
	writes := string(restored.writes)
	restored.mu.Unlock()
	if writes != "restored" {
		t.Fatalf("restored port writes = %q", writes)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSessionReconfiguresWhileWaitingToReconnect(t *testing.T) {
	first := newFakePort()
	second := newFakePort()
	ready := make(chan *Endpoint, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- New(Config{
			Port: first, ReconnectAttempts: 2, ReconnectInterval: time.Hour,
			Reconnect: func() (Port, error) { return nil, errors.New("old port missing") },
		}).Run(ctx, ready)
	}()
	e := <-ready
	_ = first.Close()
	for {
		if _, ok := (<-e.Events()).(Reconnecting); ok {
			break
		}
	}

	if err := e.Reconfigure(ctx, func() (Port, error) { return second, nil }); err != nil {
		t.Fatalf("Reconfigure() error = %v", err)
	}
	if err := e.Send(ctx, []byte("new connection")); err != nil {
		t.Fatal(err)
	}
	second.mu.Lock()
	writes := string(second.writes)
	second.mu.Unlock()
	if writes != "new connection" {
		t.Fatalf("replacement writes = %q", writes)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
