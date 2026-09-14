package rawui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
)

func TestRawFrontendHexdump(t *testing.T) {
	for _, stamp := range []string{"", "stamp"} {
		t.Run(stamp, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer writer.Close()
			endpoint := &fakeEndpoint{events: make(chan session.Event, 2), cancel: cancel}
			endpoint.events <- session.Received{Data: []byte("0123456789abcdefG")}
			endpoint.events <- session.Received{Data: []byte{0, '\r', '\n', 0x1b, 0xff}}
			close(endpoint.events)
			var output, local bytes.Buffer
			terminal := &fakeTerminal{}
			frontend := New(Config{Terminal: terminal, Input: input, Output: &output, Local: &local, Hexdump: true, TimeFormat: stamp})
			if err := frontend.Run(ctx, endpoint); err != nil {
				t.Fatal(err)
			}
			want := "00000000  30 31 32 33 34 35 36 37  38 39 61 62 63 64 65 66  |0123456789abcdef|\r\n" +
				"00000010  47                                                |G|\r\n" +
				"00000011  00 0d 0a 1b ff                                    |.....|\r\n"
			if stamp != "" {
				want = "[stamp] " + strings.ReplaceAll(strings.TrimSuffix(want, "\r\n"), "\r\n", "\r\n[stamp] ") + "\r\n"
			}
			if output.String() != want {
				t.Fatalf("output = %q, want %q", output.String(), want)
			}
			if local.Len() != 0 || !terminal.restored {
				t.Fatalf("local=%q restored=%v", local.String(), terminal.restored)
			}
		})
	}
}

type dumpShortWriter struct{ bytes.Buffer }

func (w *dumpShortWriter) Write(p []byte) (int, error) { return w.Buffer.Write(p[:min(3, len(p))]) }

type dumpErrorWriter struct{ err error }

func (w dumpErrorWriter) Write([]byte) (int, error) { return 0, w.err }

func TestHexdumpOutputFailuresAndShortWrites(t *testing.T) {
	var dst dumpShortWriter
	w := &hexWriter{dst: &dst}
	if n, err := w.Write([]byte("A")); n != 1 || err != nil {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if !strings.HasSuffix(dst.String(), "|A|\r\n") {
		t.Fatalf("output = %q", dst.String())
	}
	for _, wantErr := range []error{io.ErrClosedPipe, io.ErrShortWrite} {
		w := &hexWriter{dst: dumpErrorWriter{err: wantErr}}
		if n, err := w.Write([]byte("A")); n != 0 || !errors.Is(err, wantErr) {
			t.Fatalf("Write() = %d, %v", n, err)
		}
	}
}
