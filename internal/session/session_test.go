package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type fakePort struct {
	read      *bytes.Buffer
	write     bytes.Buffer
	blockRead bool
	readDone  chan struct{}
	closed    bool
	mu        sync.Mutex
}

func newFakePort(input []byte) *fakePort {
	return &fakePort{read: bytes.NewBuffer(input)}
}

func (p *fakePort) Read(buf []byte) (int, error) {
	if p.blockRead {
		<-p.readDone
		return 0, io.EOF
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.read.Len() == 0 {
		return 0, io.EOF
	}
	return p.read.Read(buf)
}

func (p *fakePort) Write(buf []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.write.Write(buf)
}

func (p *fakePort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blockRead && !p.closed {
		close(p.readDone)
	}
	p.closed = true
	return nil
}

func (p *fakePort) Written() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.write.Bytes()...)
}

func (p *fakePort) Closed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

type fakeTerminal struct {
	madeRaw  bool
	restored bool
}

type recordingLogger struct {
	infoEvent  string
	infoValues []any
}

func (l *recordingLogger) Info(event string, keyValues ...any) {
	l.infoEvent = event
	l.infoValues = append([]any(nil), keyValues...)
}

func (l *recordingLogger) Warn(string, ...any) {}

func (t *fakeTerminal) MakeRaw() error {
	t.madeRaw = true
	return nil
}

func (t *fakeTerminal) Restore() error {
	t.restored = true
	return nil
}

func TestSessionPassesStdinToSerial(t *testing.T) {
	port := &fakePort{
		read:      bytes.NewBuffer(nil),
		blockRead: true,
		readDone:  make(chan struct{}),
	}
	terminal := &fakeTerminal{}
	session := New(Config{
		Port:     port,
		Terminal: terminal,
		Stdin:    bytes.NewBufferString("version\r\x01q"),
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	})

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := string(port.Written()); got != "version\r" {
		t.Fatalf("serial write = %q, want %q", got, "version\r")
	}
	if !terminal.madeRaw || !terminal.restored {
		t.Fatalf("terminal madeRaw=%v restored=%v, want both true", terminal.madeRaw, terminal.restored)
	}
}

func TestSessionLogsUserRequestedClosing(t *testing.T) {
	port := &fakePort{
		read:      bytes.NewBuffer(nil),
		blockRead: true,
		readDone:  make(chan struct{}),
	}
	logger := &recordingLogger{}
	session := New(Config{
		Port:     port,
		Terminal: &fakeTerminal{},
		Stdin:    bytes.NewReader([]byte{DefaultPrefixKey, 'q'}),
		Stdout:   io.Discard,
		Stderr:   io.Discard,
		Logger:   logger,
	})

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if logger.infoEvent != "session.closing" {
		t.Fatalf("logged event = %q, want session.closing", logger.infoEvent)
	}
	if len(logger.infoValues) != 2 || logger.infoValues[0] != "reason" || logger.infoValues[1] != "user_request" {
		t.Fatalf("logged values = %#v, want reason=user_request", logger.infoValues)
	}
}

func TestSessionCopiesSerialToStdout(t *testing.T) {
	port := newFakePort([]byte("device output"))
	var stdout bytes.Buffer

	err := copySerialToOutputs(context.Background(), port, &stdout, nil, "")
	if !errors.Is(err, io.EOF) {
		t.Fatalf("copySerialToStdout() error = %v, want EOF", err)
	}

	if got := stdout.String(); got != "device output" {
		t.Fatalf("stdout = %q, want %q", got, "device output")
	}
}

func TestSessionSavesReceivedBytes(t *testing.T) {
	port := newFakePort([]byte{'a', 0, 'b', '\n'})
	var stdout bytes.Buffer
	var receiveLog bytes.Buffer

	err := copySerialToOutputs(context.Background(), port, &stdout, &receiveLog, "")
	if !errors.Is(err, io.EOF) {
		t.Fatalf("copySerialToOutputs() error = %v, want EOF", err)
	}

	want := []byte{'a', 0, 'b', '\n'}
	if got := stdout.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("stdout = %v, want %v", got, want)
	}
	if got := receiveLog.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("receive log = %v, want %v", got, want)
	}
}

func TestSessionReturnsSerialErrorAndRestoresTerminal(t *testing.T) {
	wantErr := errors.New("serial disconnected")
	port := &errorPort{err: wantErr}
	terminal := &fakeTerminal{}
	session := New(Config{
		Port:     port,
		Terminal: terminal,
		Stdin:    blockingReader{},
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	})

	err := session.Run(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	if !terminal.restored {
		t.Fatalf("terminal restored = false, want true")
	}
}

func TestPrintHelpUsesCRLFInRawMode(t *testing.T) {
	var stderr bytes.Buffer

	printHelp(&stderr)

	want := "\r\n" +
		"[[ xserial ]] local commands:\r\n" +
		"  Ctrl-A h       show this help\r\n" +
		"  Ctrl-A u       upload raw file\r\n" +
		"  Ctrl-A q       quit\r\n" +
		"  Ctrl-A Ctrl-A  send Ctrl-A\r\n"
	if got := stderr.String(); got != want {
		t.Fatalf("printHelp() = %q, want %q", got, want)
	}
}

type errorPort struct {
	err error
}

func (p *errorPort) Read([]byte) (int, error) {
	return 0, p.err
}

func (p *errorPort) Write(buf []byte) (int, error) {
	return len(buf), nil
}

func (p *errorPort) Close() error {
	return nil
}

type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) {
	time.Sleep(time.Second)
	return 0, io.EOF
}
