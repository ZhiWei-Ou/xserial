package replay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
)

func sessionWith(records ...capture.Record) capture.Session {
	start := time.Date(2026, 10, 1, 9, 55, 0, 0, time.UTC)
	for i := range records {
		records[i].Seq = uint64(i + 1)
		if records[i].At.IsZero() {
			records[i].At = start
		}
	}
	return capture.Session{Header: capture.Header{Started: start}, Records: records}
}

func TestNativeReplayPreservesShellOutputAcrossReadsWithoutDuplicatingTX(t *testing.T) {
	output := "xsh > ver\r\n\x1b[31m版本: 1\x1b[0m\r\rxsh > "
	session := sessionWith(
		capture.Record{Kind: "connected"},
		capture.Record{Kind: "tx_request", Data: []byte("ver\r")},
		capture.Record{Kind: "tx", Data: []byte("ver\r")},
		capture.Record{Kind: "rx", Data: []byte(output[:14])},
		capture.Record{Kind: "rx", Data: []byte(output[14:18])},
		capture.Record{Kind: "rx", Data: []byte(output[18:])},
		capture.Record{Kind: "closed"},
	)
	var got bytes.Buffer
	if err := Run(context.Background(), Config{Output: &got}, session); err != nil {
		t.Fatal(err)
	}
	if got.String() != output {
		t.Fatalf("native replay changed received bytes:\n got %q\nwant %q", got.String(), output)
	}
}

type testTerminal struct {
	raw, restored bool
}

func (t *testTerminal) MakeRaw() error { t.raw = true; return nil }
func (t *testTerminal) Restore() error { t.restored = true; return nil }

type observedWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	writes chan string
}

func newObservedWriter() *observedWriter {
	return &observedWriter{writes: make(chan string, 32)}
}

func (w *observedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buffer.Write(data)
	w.mu.Unlock()
	w.writes <- string(data)
	return n, err
}

func (w *observedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func awaitWrite(t *testing.T, w *observedWriter, text string) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case data := <-w.writes:
			if strings.Contains(data, text) {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %q; output %q", text, w.String())
		}
	}
}

func interactiveConfig(t *testing.T) (Config, *os.File, *testTerminal, *observedWriter, *observedWriter) {
	t.Helper()
	input, keys, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); keys.Close() })
	terminal := &testTerminal{}
	output, local := newObservedWriter(), newObservedWriter()
	return Config{Input: input, Output: output, Local: local, Terminal: terminal}, keys, terminal, output, local
}

func sendKeys(t *testing.T, keys *os.File, text string) {
	t.Helper()
	if _, err := io.WriteString(keys, text); err != nil {
		t.Fatal(err)
	}
}

func awaitRun(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replay did not finish or release its input reader")
	}
}

func TestPauseRestartCompletionAndQuitRestoreTheTerminal(t *testing.T) {
	cfg, keys, terminal, output, local := interactiveConfig(t)
	session := sessionWith(capture.Record{Kind: "rx", Data: []byte("first")}, capture.Record{Kind: "rx", Data: []byte("second")})
	session.Records[1].At = session.Header.Started.Add(120 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, session) }()
	awaitWrite(t, output, "first")
	sendKeys(t, keys, " ")
	awaitWrite(t, local, "paused")
	select {
	case data := <-output.writes:
		t.Fatalf("paused replay emitted %q", data)
	case <-time.After(160 * time.Millisecond):
	}
	sendKeys(t, keys, " ")
	awaitWrite(t, local, "playing")
	awaitWrite(t, output, "second")
	awaitWrite(t, local, "complete")
	// Terminal cursor reports contain R; unsupported controls must not restart
	// playback or open any editing interface. Space at completion is a no-op.
	sendKeys(t, keys, "\x1b[1;20R +-\t\x10\r")
	select {
	case data := <-output.writes:
		t.Fatalf("unsupported input changed completed playback: %q", data)
	case <-time.After(30 * time.Millisecond):
	}
	sendKeys(t, keys, "r")
	awaitWrite(t, output, "\x1b[2J")
	awaitWrite(t, output, "first")
	awaitWrite(t, output, "second")
	awaitWrite(t, local, "complete")
	sendKeys(t, keys, "\x03")
	awaitRun(t, done)
	if !terminal.raw || !terminal.restored {
		t.Fatalf("terminal lifecycle: raw=%v restored=%v", terminal.raw, terminal.restored)
	}
	if strings.Count(output.String(), "first") != 2 || strings.Count(output.String(), "second") != 2 {
		t.Fatalf("restart did not replay exactly once: %q", output.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOutputFailureAndCancellationReleaseTerminalAndBlockedInput(t *testing.T) {
	for _, name := range []string{"output_failure", "context_cancel"} {
		t.Run(name, func(t *testing.T) {
			failure := name == "output_failure"
			cfg, _, terminal, _, local := interactiveConfig(t)
			session := sessionWith(capture.Record{Kind: "rx", Data: []byte("output")})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure {
				cfg.Output = failingWriter{}
			} else {
				session.Records[0].At = session.Header.Started.Add(time.Hour)
			}
			done := make(chan error, 1)
			go func() { done <- Run(ctx, cfg, session) }()
			awaitWrite(t, local, "playing")
			if !failure {
				cancel()
			}
			select {
			case err := <-done:
				if failure && !errors.Is(err, io.ErrClosedPipe) {
					t.Fatalf("output failure = %v", err)
				}
				if !failure && err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("replay leaked blocked input or timer")
			}
			if !terminal.restored {
				t.Fatal("terminal was not restored")
			}
		})
	}
}

func TestClosedInputFinishesAPausedReplay(t *testing.T) {
	cfg, keys, _, output, local := interactiveConfig(t)
	session := sessionWith(capture.Record{Kind: "rx", Data: []byte("first")}, capture.Record{Kind: "rx", Data: []byte("tail")})
	session.Records[1].At = session.Header.Started.Add(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, session) }()
	awaitWrite(t, output, "first")
	sendKeys(t, keys, " ")
	awaitWrite(t, local, "paused")
	keys.Close()
	awaitRun(t, done)
	if !strings.Contains(output.String(), "tail") {
		t.Fatalf("closing input lost the remaining output: %q", output.String())
	}
}
