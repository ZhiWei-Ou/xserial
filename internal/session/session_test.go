package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
)

type fakePort struct {
	read       *bytes.Buffer
	written    bytes.Buffer
	blockRead  bool
	readDone   chan struct{}
	closeOnce  sync.Once
	mu         sync.Mutex
	shortWrite int
}

func newBlockingPort() *fakePort {
	return &fakePort{read: bytes.NewBuffer(nil), blockRead: true, readDone: make(chan struct{})}
}

func (p *fakePort) Read(data []byte) (int, error) {
	if p.blockRead {
		<-p.readDone
		return 0, io.EOF
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.read.Read(data)
}

func (p *fakePort) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(data)
	if p.shortWrite > 0 && n > p.shortWrite {
		n = p.shortWrite
	}
	return p.written.Write(data[:n])
}

func (p *fakePort) Close() error {
	p.closeOnce.Do(func() {
		if p.blockRead {
			close(p.readDone)
		}
	})
	return nil
}

func (p *fakePort) Written() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.written.String()
}

type frontendFunc func(context.Context, Endpoint) error

func (f frontendFunc) Run(ctx context.Context, endpoint Endpoint) error { return f(ctx, endpoint) }

func TestSessionRoutesFrontendWritesThroughFullWriter(t *testing.T) {
	port := newBlockingPort()
	port.shortWrite = 2
	frontend := frontendFunc(func(ctx context.Context, endpoint Endpoint) error {
		if err := endpoint.Send(ctx, []byte("abcdef")); err != nil {
			return err
		}
		endpoint.Quit()
		return nil
	})

	err := New(Config{Port: port, Frontend: frontend}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := port.Written(); got != "abcdef" {
		t.Fatalf("serial output = %q", got)
	}
}

func TestSessionRecordsAndDeliversRawReceivedBytes(t *testing.T) {
	want := []byte{'a', 0, 'b', '\n'}
	port := &fakePort{read: bytes.NewBuffer(want)}
	var log bytes.Buffer
	var received []byte
	frontend := frontendFunc(func(ctx context.Context, endpoint Endpoint) error {
		for event := range endpoint.Events() {
			if event, ok := event.(Received); ok {
				received = append(received, event.Data...)
			}
		}
		return nil
	})

	err := New(Config{Port: port, Frontend: frontend, ReceiveLog: &log}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Equal(received, want) || !bytes.Equal(log.Bytes(), want) {
		t.Fatalf("received=%v log=%v want=%v", received, log.Bytes(), want)
	}
}

func TestSessionReturnsSerialErrorAfterFrontendStops(t *testing.T) {
	want := errors.New("serial disconnected")
	port := &errorPort{err: want}
	frontendStopped := make(chan struct{})
	frontend := frontendFunc(func(ctx context.Context, endpoint Endpoint) error {
		<-ctx.Done()
		close(frontendStopped)
		return nil
	})

	err := New(Config{Port: port, Frontend: frontend}).Run(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}
	select {
	case <-frontendStopped:
	default:
		t.Fatal("frontend was not stopped before Run returned")
	}
}

func TestUploadExcludesNormalWritesAndFinishesBeforeRunReturns(t *testing.T) {
	file := t.TempDir() + "/firmware.bin"
	if err := os.WriteFile(file, bytes.Repeat([]byte{0xaa}, 512), 0o600); err != nil {
		t.Fatal(err)
	}
	port := newGatedPort()
	var finished UploadFinished
	frontend := frontendFunc(func(ctx context.Context, endpoint Endpoint) error {
		if err := endpoint.StartUpload(ctx, file); err != nil {
			return err
		}
		<-port.writeStarted
		if err := endpoint.Send(ctx, []byte("user input")); !errors.Is(err, ErrTransferActive) {
			return errors.New("normal write was not rejected during upload")
		}
		close(port.allowWrite)
		for event := range endpoint.Events() {
			if event, ok := event.(UploadFinished); ok {
				finished = event
				endpoint.Quit()
				return nil
			}
		}
		return nil
	})

	if err := New(Config{Port: port, Frontend: frontend}).Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if finished.Err != nil || finished.Bytes != 512 {
		t.Fatalf("upload finished = %#v", finished)
	}
	if port.concurrentWrite {
		t.Fatal("serial port observed concurrent writes")
	}
}

type gatedPort struct {
	*fakePort
	writeStarted    chan struct{}
	allowWrite      chan struct{}
	startOnce       sync.Once
	writeMu         sync.Mutex
	writing         bool
	concurrentWrite bool
}

func newGatedPort() *gatedPort {
	return &gatedPort{
		fakePort:     newBlockingPort(),
		writeStarted: make(chan struct{}),
		allowWrite:   make(chan struct{}),
	}
}

func (p *gatedPort) Write(data []byte) (int, error) {
	p.writeMu.Lock()
	if p.writing {
		p.concurrentWrite = true
	}
	p.writing = true
	p.writeMu.Unlock()
	p.startOnce.Do(func() { close(p.writeStarted) })
	<-p.allowWrite
	n, err := p.fakePort.Write(data)
	p.writeMu.Lock()
	p.writing = false
	p.writeMu.Unlock()
	return n, err
}

type errorPort struct{ err error }

func (p *errorPort) Read([]byte) (int, error)       { return 0, p.err }
func (p *errorPort) Write(data []byte) (int, error) { return len(data), nil }
func (p *errorPort) Close() error                   { return nil }
