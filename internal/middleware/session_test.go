package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/transfer"
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

func TestSessionComposesConfiguredHandlers(t *testing.T) {
	port := newBlockingPort()
	handler := testHandler{name: "suffix", outbound: func(envelope Envelope) (Action, error) {
		envelope.Data = append(envelope.Data, 0xff)
		return Forward(envelope), nil
	}}
	frontend := frontendFunc(func(ctx context.Context, endpoint Endpoint) error {
		if err := endpoint.Send(ctx, []byte{0x01}); err != nil {
			return err
		}
		endpoint.Quit()
		return nil
	})

	if err := New(Config{Port: port, Frontend: frontend, Handlers: []Handler{handler}}).Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := []byte(port.Written()); !bytes.Equal(got, []byte{0x01, 0xff}) {
		t.Fatalf("serial output = %v", got)
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
		_ = endpoint.Events()
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

func TestSessionReconnectsAfterSuccessfulInitialConnection(t *testing.T) {
	disconnectErr := errors.New("device unplugged")
	first := &errorPort{err: disconnectErr}
	second := newBlockingPort()
	var attempts atomic.Int32
	open := func() (SerialPort, error) {
		if attempts.Add(1) == 1 {
			return nil, errors.New("device is still missing")
		}
		return second, nil
	}
	frontend := frontendFunc(func(ctx context.Context, endpoint Endpoint) error {
		for event := range endpoint.Events() {
			if _, ok := event.(Reconnected); ok {
				if err := endpoint.Send(ctx, []byte("connected again")); err != nil {
					return err
				}
				endpoint.Quit()
				return nil
			}
		}
		return nil
	})

	err := New(Config{
		Port: first, Reconnect: open, ReconnectInterval: time.Nanosecond,
		Frontend: frontend,
	}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("reconnect attempts = %d, want 2", attempts.Load())
	}
	if got := second.Written(); got != "connected again" {
		t.Fatalf("reconnected serial output = %q", got)
	}
}

func TestSessionAppliesRuntimeConnectionConfiguration(t *testing.T) {
	first := newBlockingPort()
	second := newBlockingPort()
	want := ConnectionConfig{PortName: "/dev/test1", BaudRate: 921600, DataBits: 8, Parity: "none", StopBits: "1"}
	var got ConnectionConfig
	frontend := frontendFunc(func(ctx context.Context, endpoint Endpoint) error {
		configurable, ok := endpoint.(interface {
			Configure(context.Context, ConnectionConfig) error
		})
		if !ok {
			return errors.New("endpoint is not configurable")
		}
		if err := configurable.Configure(ctx, want); err != nil {
			return err
		}
		if err := endpoint.Send(ctx, []byte("configured")); err != nil {
			return err
		}
		endpoint.Quit()
		return nil
	})

	err := New(Config{
		Port: first,
		Reconnect: func() (SerialPort, error) {
			return newBlockingPort(), nil
		},
		OpenConnection: func(cfg ConnectionConfig) (SerialPort, error) {
			got = cfg
			return second, nil
		},
		Frontend: frontend,
	}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got != want {
		t.Fatalf("configuration = %#v, want %#v", got, want)
	}
	if output := second.Written(); output != "configured" {
		t.Fatalf("replacement output = %q", output)
	}
}

func TestSessionQuitCancelsReconnectWait(t *testing.T) {
	var attempts atomic.Int32
	frontend := frontendFunc(func(ctx context.Context, endpoint Endpoint) error {
		for event := range endpoint.Events() {
			if _, ok := event.(Disconnected); ok {
				endpoint.Quit()
				return nil
			}
		}
		return nil
	})

	err := New(Config{
		Port: &errorPort{err: errors.New("device unplugged")},
		Reconnect: func() (SerialPort, error) {
			attempts.Add(1)
			return nil, errors.New("device missing")
		},
		ReconnectInterval: time.Hour,
		Frontend:          frontend,
	}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if attempts.Load() != 0 {
		t.Fatalf("reconnect attempts = %d, want 0", attempts.Load())
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

func TestSessionRunsYMODEMUploadThroughSharedSerialReaderAndWriter(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	source := filepath.Join(sourceDir, "firmware.bin")
	want := bytes.Repeat([]byte("firmware-"), 200)
	if err := os.WriteFile(source, want, 0o600); err != nil {
		t.Fatal(err)
	}

	sessionConn, devicePort := net.Pipe()
	sessionPort := ymodemTestPort{Conn: sessionConn}
	defer devicePort.Close()
	receiveErr := make(chan error, 1)
	go func() {
		_, _, err := transfer.ReceiveYMODEMFile(context.Background(), destinationDir, devicePort, nil, nil)
		receiveErr <- err
	}()
	frontend := frontendFunc(func(ctx context.Context, endpoint Endpoint) error {
		if err := endpoint.StartYMODEMUpload(ctx, source); err != nil {
			return err
		}
		for event := range endpoint.Events() {
			if event, ok := event.(YMODEMFinished); ok {
				if event.Err != nil {
					return event.Err
				}
				endpoint.Quit()
				return nil
			}
		}
		return nil
	})

	if err := New(Config{Port: sessionPort, Frontend: frontend}).Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := <-receiveErr; err != nil {
		t.Fatalf("device receive error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(destinationDir, "firmware.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("device received bytes do not match source")
	}
}

type ymodemTestPort struct {
	net.Conn
}

func (p ymodemTestPort) Read(data []byte) (int, error) {
	n, err := p.Conn.Read(data)
	if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		err = io.EOF
	}
	return n, err
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
