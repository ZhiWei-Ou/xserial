package rawui

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/demo"
	"github.com/ZhiWei-Ou/xserial/internal/logging"
	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
)

type readyTerminal struct {
	fakeTerminal
	ready chan struct{}
}

func (t *readyTerminal) MakeRaw() error { err := t.fakeTerminal.MakeRaw(); close(t.ready); return err }

type receivedOutput struct {
	mu sync.Mutex
	bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (w *receivedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.Buffer.Write(data)
	w.once.Do(func() { close(w.ready) })
	return n, err
}

func TestRemoteSendPreservesRawDeviceBytesAndRestore(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	terminal := &readyTerminal{ready: make(chan struct{})}
	output := &receivedOutput{ready: make(chan struct{})}
	var local bytes.Buffer
	logger := logging.New(&local)
	debug := debugsession.New(debugsession.Connection{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	frontend := New(Config{Terminal: terminal, Input: input, Output: output, Local: logger})
	go func() {
		done <- session.New(session.Config{Port: demo.NewPort(), Debug: debug, Frontend: frontend, Logger: logger}).Run(ctx)
	}()
	<-terminal.ready
	status := debug.Status()
	sent, err := debug.Send(ctx, debugsession.SendInput{SessionID: status.SessionID, Data: "01 03 00 00 00 02 C4 0B", Encoding: "hex"})
	if err != nil || sent.Delivery != "written" {
		t.Fatalf("send = %+v, %v", sent, err)
	}
	<-output.ready
	if _, err := writer.Write([]byte{DefaultPrefixKey, 'q'}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !terminal.restored {
		t.Fatal("terminal was not restored")
	}
	want := []byte{1, 3, 4, 0, 100, 0, 101, 0x7b, 0xc7}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("stdout = %x", output.Bytes())
	}
	if local.String() != "\r\n" {
		t.Fatalf("stderr = %q", local.String())
	}
}
