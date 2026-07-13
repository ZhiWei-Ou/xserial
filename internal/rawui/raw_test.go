package rawui

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/logging"
	"github.com/ZhiWei-Ou/xserial/internal/session"
)

type fakeTerminal struct {
	raw, restored bool
}

func (t *fakeTerminal) MakeRaw() error { t.raw = true; return nil }
func (t *fakeTerminal) Restore() error { t.restored = true; return nil }

type signalWriter struct {
	bytes.Buffer
	match []byte
	done  chan struct{}
	once  sync.Once
}

func (w *signalWriter) Write(data []byte) (int, error) {
	n, err := w.Buffer.Write(data)
	if bytes.Contains(w.Bytes(), w.match) {
		w.once.Do(func() { close(w.done) })
	}
	return n, err
}

type fakeEndpoint struct {
	events         chan session.Event
	cancel         context.CancelFunc
	mu             sync.Mutex
	sent           bytes.Buffer
	rawUpload      string
	ymodemUpload   string
	ymodemDownload string
	activeTransfer string
	autoFinish     bool
	canceled       int
}

func (e *fakeEndpoint) Events() <-chan session.Event { return e.events }
func (e *fakeEndpoint) Send(_ context.Context, data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.sent.Write(data)
	return nil
}
func (e *fakeEndpoint) StartUpload(_ context.Context, path string) error {
	e.rawUpload = path
	e.activeTransfer = "raw"
	return nil
}
func (e *fakeEndpoint) StartYMODEMUpload(_ context.Context, path string) error {
	e.ymodemUpload = path
	e.activeTransfer = "ymodem"
	if e.autoFinish {
		e.events <- session.YMODEMFinished{Direction: "upload", Path: path}
	}
	return nil
}
func (e *fakeEndpoint) StartYMODEMDownload(_ context.Context, dir string) error {
	e.ymodemDownload = dir
	e.activeTransfer = "ymodem-download"
	if e.autoFinish {
		e.events <- session.YMODEMFinished{Direction: "download", Path: "received.bin"}
	}
	return nil
}
func (e *fakeEndpoint) CancelTransfer() {
	e.canceled++
	switch e.activeTransfer {
	case "raw":
		e.events <- session.UploadFinished{Err: context.Canceled}
	case "ymodem":
		e.events <- session.YMODEMFinished{Direction: "upload", Err: context.Canceled}
	case "ymodem-download":
		e.events <- session.YMODEMFinished{Direction: "download", Err: context.Canceled}
	}
	e.activeTransfer = ""
}
func (e *fakeEndpoint) Quit() { e.cancel() }

func TestRawFrontendUsesCtrlPAndRestoresTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{events: make(chan session.Event), cancel: cancel}
	terminal := &fakeTerminal{}
	var local bytes.Buffer
	frontend := New(Config{
		Terminal: terminal,
		Input:    bytes.NewReader([]byte{'v', DefaultPrefixKey, DefaultPrefixKey, DefaultPrefixKey, 'h', DefaultPrefixKey, 'q'}),
		Output:   &bytes.Buffer{},
		Local:    &local,
	})

	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !terminal.raw || !terminal.restored {
		t.Fatalf("terminal raw=%v restored=%v", terminal.raw, terminal.restored)
	}
	endpoint.mu.Lock()
	got := endpoint.sent.String()
	endpoint.mu.Unlock()
	if got != string([]byte{'v', DefaultPrefixKey}) {
		t.Fatalf("serial output = %v", []byte(got))
	}
	if !bytes.Contains(local.Bytes(), []byte("Ctrl-P h")) {
		t.Fatalf("help = %q", local.String())
	}
}

func TestRawFrontendWritesReceivedBytesTransparently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{events: make(chan session.Event, 1), cancel: cancel}
	input, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer inputWriter.Close()
	want := []byte{'a', 0, 'b', '\n'}
	endpoint.events <- session.Received{Data: want}
	close(endpoint.events)
	terminal := &fakeTerminal{}
	var output bytes.Buffer
	frontend := New(Config{Terminal: terminal, Input: input, Output: &output, Local: &bytes.Buffer{}})

	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("output = %v, want %v", output.Bytes(), want)
	}
}

func TestRawFrontendStartsYMODEMTransfersWithControlShortcuts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{events: make(chan session.Event, 2), cancel: cancel, autoFinish: true}
	frontend := New(Config{
		Terminal: &fakeTerminal{},
		Input: bytes.NewReader([]byte{
			DefaultPrefixKey, 0x15,
			'f', 'i', 'r', 'm', 'w', 'a', 'r', 'e', '.', 'b', 'i', 'n', '\r',
			DefaultPrefixKey, 0x04,
			DefaultPrefixKey, 'q',
		}),
		Output: &bytes.Buffer{},
		Local:  &bytes.Buffer{},
	})

	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if endpoint.ymodemUpload != "firmware.bin" {
		t.Fatalf("YMODEM upload path = %q", endpoint.ymodemUpload)
	}
	if endpoint.ymodemDownload != "." {
		t.Fatalf("YMODEM download directory = %q", endpoint.ymodemDownload)
	}
}

func TestRawFrontendEscapesFilePathPrompts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{events: make(chan session.Event, 2), cancel: cancel}
	frontend := New(Config{
		Terminal: &fakeTerminal{},
		Input: bytes.NewReader([]byte{
			DefaultPrefixKey, 'u', 0x1b, 'r',
			DefaultPrefixKey, 0x15, 0x1b, 'y',
			DefaultPrefixKey, 'q',
		}),
		Output: &bytes.Buffer{},
		Local:  &bytes.Buffer{},
	})

	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if endpoint.rawUpload != "" || endpoint.ymodemUpload != "" {
		t.Fatalf("uploads started: raw=%q YMODEM=%q", endpoint.rawUpload, endpoint.ymodemUpload)
	}
	if got := endpoint.sent.String(); got != "ry" {
		t.Fatalf("serial output = %q, want %q", got, "ry")
	}
}

func TestRawFrontendEscCancelsActiveTransfers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{events: make(chan session.Event, 2), cancel: cancel}
	frontend := New(Config{
		Terminal: &fakeTerminal{},
		Input: bytes.NewReader([]byte{
			DefaultPrefixKey, 'u', 'r', 'a', 'w', '.', 'b', 'i', 'n', '\r', 0x1b,
			DefaultPrefixKey, 0x15, 'f', 'w', '.', 'b', 'i', 'n', '\r', 0x1b,
			DefaultPrefixKey, 0x04, 0x1b,
			DefaultPrefixKey, 'q',
		}),
		Output: &bytes.Buffer{},
		Local:  &bytes.Buffer{},
	})

	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if endpoint.canceled != 3 {
		t.Fatalf("CancelTransfer() calls = %d, want 3", endpoint.canceled)
	}
}

func TestRawFrontendHidesDeviceBytesDuringYMODEMFileSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{events: make(chan session.Event), cancel: cancel}
	input, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer inputWriter.Close()
	prompt := &signalWriter{match: []byte("YMODEM UPLOAD ] file"), done: make(chan struct{})}
	var output bytes.Buffer
	frontend := New(Config{Terminal: &fakeTerminal{}, Input: input, Output: &output, Local: prompt})
	result := make(chan error, 1)
	go func() { result <- frontend.Run(ctx, endpoint) }()

	if _, err := inputWriter.Write([]byte{DefaultPrefixKey, 0x15}); err != nil {
		t.Fatal(err)
	}
	<-prompt.done
	endpoint.events <- session.Received{Data: []byte("CCC")}
	if _, err := inputWriter.Write([]byte{0x1b, DefaultPrefixKey, 'q'}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := output.String(); got != "" {
		t.Fatalf("device output during YMODEM file selection = %q", got)
	}
}

func TestRawFrontendEndsCompletedProgressBeforeLoggerWrites(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{events: make(chan session.Event), cancel: cancel}
	input, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer inputWriter.Close()
	local := &signalWriter{match: []byte("2672/2672 bytes"), done: make(chan struct{})}
	frontend := New(Config{Terminal: &fakeTerminal{}, Input: input, Output: &bytes.Buffer{}, Local: local})
	result := make(chan error, 1)
	go func() { result <- frontend.Run(ctx, endpoint) }()

	endpoint.events <- session.YMODEMProgress{Direction: "upload", Written: 2672, Total: 2672}
	<-local.done
	logging.New(local).Info("transfer.ymodem_completed", "bytes", 2672)
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := local.String(); !bytes.Contains([]byte(got), []byte("2672/2672 bytes\r\n[ INFO")) {
		t.Fatalf("progress and log share one line: %q", got)
	}
}

func TestRawFrontendDisplaysYMODEMChecksumAndFrameStats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{events: make(chan session.Event, 1), cancel: cancel}
	endpoint.events <- session.YMODEMFinished{
		Direction: "upload", Path: "README.md", Bytes: 6913, CRC32: 0x1234abcd,
		FailedFrames: 2, RetriedFrames: 2,
	}
	close(endpoint.events)
	input, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer inputWriter.Close()
	var local bytes.Buffer
	frontend := New(Config{Terminal: &fakeTerminal{}, Input: input, Output: &bytes.Buffer{}, Local: &local})

	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "[ YMODEM UPLOAD ] completed: README.md bytes=6913 crc32=1234abcd failed_frames=2 retried_frames=2\r\n"
	if got := local.String(); got != want {
		t.Fatalf("local output = %q, want %q", got, want)
	}
}
