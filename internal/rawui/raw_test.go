package rawui

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/session"
)

type fakeTerminal struct {
	raw, restored bool
}

func (t *fakeTerminal) MakeRaw() error { t.raw = true; return nil }
func (t *fakeTerminal) Restore() error { t.restored = true; return nil }

type fakeEndpoint struct {
	events chan session.Event
	cancel context.CancelFunc
	mu     sync.Mutex
	sent   bytes.Buffer
}

func (e *fakeEndpoint) Events() <-chan session.Event { return e.events }
func (e *fakeEndpoint) Send(_ context.Context, data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.sent.Write(data)
	return nil
}
func (e *fakeEndpoint) StartUpload(context.Context, string) error { return nil }
func (e *fakeEndpoint) CancelUpload()                             {}
func (e *fakeEndpoint) Quit()                                     { e.cancel() }

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
	want := []byte{'a', 0, 'b', '\n'}
	endpoint.events <- session.Received{Data: want}
	close(endpoint.events)
	terminal := &fakeTerminal{}
	var output bytes.Buffer
	frontend := New(Config{Terminal: terminal, Input: bytes.NewReader(nil), Output: &output, Local: &bytes.Buffer{}})

	if err := frontend.Run(ctx, endpoint); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("output = %v, want %v", output.Bytes(), want)
	}
}
