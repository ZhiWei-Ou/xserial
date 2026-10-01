package backend

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// The read stops on Close, while a driver write can finish later. This lets
// reconfiguration complete while the writer is still unwinding the old I/O.
type delayedWriterPort struct {
	*fakePort
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *delayedWriterPort) Write(data []byte) (int, error) {
	p.once.Do(func() { close(p.started) })
	<-p.release
	return p.fakePort.Write(data)
}

func TestQueuedRemoteRequestCannotWriteReplacementConnection(t *testing.T) {
	first := &delayedWriterPort{fakePort: newFakePort(), started: make(chan struct{}), release: make(chan struct{})}
	second := newFakePort()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan *Endpoint, 1)
	done := make(chan error, 1)
	go func() { done <- New(Config{Port: first}).Run(ctx, ready) }()
	e := <-ready
	writeDone := make(chan error, 1)
	go func() { writeDone <- e.Send(ctx, []byte("old driver write")) }()
	<-first.started
	remoteDone := make(chan error, 1)
	go func() { remoteDone <- e.SendGeneration(ctx, 1, []byte("obsolete command"), nil) }()
	if err := e.Reconfigure(ctx, func() (Port, error) { return second, nil }); err != nil {
		t.Fatal(err)
	}
	close(first.release)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-remoteDone; !errors.Is(err, ErrStaleConnection) {
		t.Fatalf("queued request = %v", err)
	}
	if err := e.Send(ctx, []byte("current command")); err != nil {
		t.Fatal(err)
	}
	second.mu.Lock()
	got := string(second.writes)
	second.mu.Unlock()
	if got != "current command" {
		t.Fatalf("new connection = %q", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCanceledQueuedWriteDoesNotReachDriver(t *testing.T) {
	port := &delayedWriterPort{fakePort: newFakePort(), started: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan *Endpoint, 1)
	done := make(chan error, 1)
	go func() { done <- New(Config{Port: port}).Run(ctx, ready) }()
	e := <-ready
	firstDone := make(chan error, 1)
	go func() { firstDone <- e.Send(ctx, []byte("first")) }()
	<-port.started
	writeCtx, stop := context.WithCancel(ctx)
	stop()
	if err := e.SendGeneration(writeCtx, 1, []byte("canceled"), nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(port.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := e.Send(ctx, []byte("last")); err != nil {
		t.Fatal(err)
	}
	port.mu.Lock()
	got := string(port.writes)
	port.mu.Unlock()
	if got != "firstlast" {
		t.Fatal(got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type cancelShortPort struct {
	*fakePort
	cancel context.CancelFunc
	once   sync.Once
}

func (p *cancelShortPort) Write(data []byte) (int, error) {
	n, err := p.fakePort.Write(data[:1])
	p.once.Do(p.cancel)
	return n, err
}

func TestCancellationBetweenShortWritesKeepsConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writeCtx, stopWrite := context.WithCancel(ctx)
	defer stopWrite()
	port := &cancelShortPort{fakePort: newFakePort(), cancel: stopWrite}
	ready := make(chan *Endpoint, 1)
	done := make(chan error, 1)
	go func() { done <- New(Config{Port: port}).Run(ctx, ready) }()
	e := <-ready
	if err := e.Send(writeCtx, []byte("canceled remainder")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := e.Send(ctx, []byte("next")); err != nil {
		t.Fatal("cancellation disconnected port:", err)
	}
	port.mu.Lock()
	got := string(port.writes)
	port.mu.Unlock()
	if got != "cnext" {
		t.Fatalf("driver writes=%q", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
