package backend

import (
	"context"
	"sync/atomic"
	"testing"
)

type countedPort struct {
	*fakePort
	closes atomic.Int32
}

func (p *countedPort) Close() error { p.closes.Add(1); return p.fakePort.Close() }

func TestConnectionIsClosedOnceOnShutdownAndEarlyCancellation(t *testing.T) {
	for _, early := range []bool{false, true} {
		port := &countedPort{fakePort: newFakePort()}
		ctx, cancel := context.WithCancel(context.Background())
		ready := make(chan *Endpoint)
		if early {
			cancel()
		}
		done := make(chan error, 1)
		go func() { done <- New(Config{Port: port}).Run(ctx, ready) }()
		if !early {
			<-ready
			cancel()
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if count := port.closes.Load(); count != 1 {
			t.Fatalf("early=%v, closes=%d", early, count)
		}
	}
}
