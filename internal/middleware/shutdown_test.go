package middleware

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"
)

type finiteTrafficPort struct {
	remaining int
	eof       chan struct{}
	once      sync.Once
}

func (p *finiteTrafficPort) Read(data []byte) (int, error) {
	if p.remaining == 0 {
		p.once.Do(func() { close(p.eof) })
		return 0, io.EOF
	}
	p.remaining--
	data[0] = byte(p.remaining)
	return 1, nil
}
func (*finiteTrafficPort) Write(data []byte) (int, error) { return len(data), nil }
func (*finiteTrafficPort) Close() error                   { return nil }

func TestFrontendExitCannotLeaveDispatcherBlockedOnFullUIQueue(t *testing.T) {
	port := &finiteTrafficPort{remaining: 50, eof: make(chan struct{})}
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error {
		e.Events() // Mark ready, then model a UI that exits without draining traffic.
		select {
		case <-port.eof:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := New(Config{Port: port, Frontend: frontend}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("session required the timeout to break an abandoned queue")
	}
}

func TestAlreadyCanceledSessionClosesPortBeforeReturning(t *testing.T) {
	port := newBlockingPort()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	frontend := frontendFunc(func(ctx context.Context, e Endpoint) error { <-ctx.Done(); return ctx.Err() })
	if err := New(Config{Port: port, Frontend: frontend}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-port.readDone:
	default:
		t.Fatal("early cancellation left the port open")
	}
}
