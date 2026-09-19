package rawui

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"

	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
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
	sendErr        error
}

func (e *fakeEndpoint) Events() <-chan session.Event { return e.events }
func (e *fakeEndpoint) Send(_ context.Context, data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sendErr != nil {
		return e.sendErr
	}
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
	if !bytes.Contains(local.Bytes(), []byte("Ctrl-P h")) || !bytes.Contains(local.Bytes(), []byte("Ctrl-P i")) {
		t.Fatalf("help = %q", local.String())
	}
}

func TestRawFrontendShowsConfigurationOnRequest(t *testing.T) {
	for _, input := range []string{"x", "\x10ix", "\x10Ix"} {
		t.Run(input, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			endpoint := &fakeEndpoint{events: make(chan session.Event), cancel: cancel}
			var local, output bytes.Buffer
			frontend := New(Config{
				Terminal:   &fakeTerminal{},
				Input:      bytes.NewBufferString(input),
				Output:     &output,
				Local:      &local,
				Connection: session.ConnectionConfig{PortName: "/dev/test", BaudRate: 57600, DataBits: 7, Parity: "even", StopBits: "2"},
			})
			if err := frontend.Run(ctx, endpoint); err != nil {
				t.Fatal(err)
			}
			want := ""
			if input != "x" {
				want = "\r\nPort: /dev/test  Baud: 57600  Data bits: 7  Parity: even  Stop bits: 2\r\n"
			}
			if local.String() != want {
				t.Fatalf("local output = %q, want %q", local.String(), want)
			}
			if output.Len() != 0 || endpoint.sent.String() != "x" {
				t.Fatalf("device output = %q, serial input = %q", output.String(), endpoint.sent.String())
			}
		})
	}
}

func TestRawFrontendCanQuitWhileSerialPortIsDisconnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{
		events:  make(chan session.Event),
		cancel:  cancel,
		sendErr: session.ErrDisconnected,
	}
	terminal := &fakeTerminal{}
	frontend := New(Config{
		Terminal: terminal,
		Input:    bytes.NewReader([]byte{'x', DefaultPrefixKey, 'q'}),
		Output:   &bytes.Buffer{},
		Local:    &bytes.Buffer{},
	})

	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !terminal.restored {
		t.Fatal("terminal was not restored")
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

func TestRawFrontendPrefixesReceivedLinesWhenTimeIsEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	endpoint := &fakeEndpoint{events: make(chan session.Event, 2), cancel: cancel}
	input, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer inputWriter.Close()
	endpoint.events <- session.Received{Data: []byte("first\r")}
	endpoint.events <- session.Received{Data: []byte("\nsecond")}
	close(endpoint.events)

	var output bytes.Buffer
	frontend := New(Config{
		Terminal:   &fakeTerminal{},
		Input:      input,
		Output:     &output,
		Local:      &bytes.Buffer{},
		TimeFormat: "stamp",
	})
	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if want := "[stamp] first\r\n[stamp] second"; output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
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
	prompt := &signalWriter{match: []byte("YMODEM upload file"), done: make(chan struct{})}
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

func TestRawFrontendEndsProgressBeforeCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := &fakeEndpoint{events: make(chan session.Event, 2), cancel: cancel}
	endpoint.events <- session.UploadProgress{Written: 2048, Total: 2672}
	endpoint.events <- session.UploadFinished{Bytes: 2672}
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
	if got := local.String(); !bytes.Contains([]byte(got), []byte("2048/2672 bytes\r\nUploaded 2672 bytes\r\n")) {
		t.Fatalf("progress and completion share one line: %q", got)
	}
}

func TestRawFrontendDisplaysConciseYMODEMResult(t *testing.T) {
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
	want := "Sent README.md (6913 bytes, CRC32 1234abcd, 2 retries)\r\n"
	if got := local.String(); got != want {
		t.Fatalf("local output = %q, want %q", got, want)
	}
}
