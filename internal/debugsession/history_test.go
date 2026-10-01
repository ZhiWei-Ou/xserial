package debugsession

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func ptr(n int) *int { return &n }
func raw(t *testing.T, result ReadResult) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(result.DataBase64)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReadPaginationReplayAndHistoryGap(t *testing.T) {
	h := newHistory("session", 8)
	h.append([]byte("abcdefghijkl"))
	in := ReadInput{Cursor: "session:0", WaitMS: ptr(0), MaxBytes: ptr(4)}
	first, err := h.read(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if raw(t, first) != "efgh" || first.Reason != "gap" || first.DroppedBytes != 4 || !first.HasMore || first.NextCursor != "session:8" {
		t.Fatalf("first = %+v", first)
	}
	again, err := h.read(context.Background(), in)
	if err != nil || again != first {
		t.Fatalf("repeated read = %+v, %v; want %+v", again, err, first)
	}
	in.Cursor = first.NextCursor
	second, err := h.read(context.Background(), in)
	if err != nil || raw(t, second) != "ijkl" || second.HasMore || second.DroppedBytes != 0 {
		t.Fatalf("second = %+v, %v", second, err)
	}
	checkpoint, err := h.read(context.Background(), ReadInput{Cursor: "now", WaitMS: ptr(0)})
	if err != nil || checkpoint.Bytes != 0 || checkpoint.NextCursor != "session:12" {
		t.Fatalf("checkpoint = %+v, %v", checkpoint, err)
	}
}

func TestReadTailsAndPreservesBinary(t *testing.T) {
	h := newHistory("s", 128)
	h.append([]byte("earlier\r\n\x1b[31merror\x1b[0m\r\nprompt>\x00\xff"))
	result, err := h.read(context.Background(), ReadInput{WaitMS: ptr(0)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "earlier\nerror\nprompt>\\x00\uFFFD" {
		t.Fatalf("display = %q", result.Output)
	}
	if got := raw(t, result); got != "earlier\r\n\x1b[31merror\x1b[0m\r\nprompt>\x00\xff" {
		t.Fatalf("raw = %q", got)
	}
	tail, err := h.read(context.Background(), ReadInput{WaitMS: ptr(0), MaxBytes: ptr(2)})
	if err != nil || raw(t, tail) != "\x00\xff" || tail.DroppedBytes != 0 {
		t.Fatalf("tail = %+v, %v", tail, err)
	}
}

func TestReadWaitsForDataAndReturnsAfterIdle(t *testing.T) {
	h := newHistory("s", 128)
	done := make(chan ReadResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := h.read(context.Background(), ReadInput{Cursor: "s:0", WaitMS: ptr(1000), IdleMS: ptr(5)})
		done <- result
		errCh <- err
	}()
	h.append([]byte("split "))
	h.append([]byte("response>"))
	select {
	case result := <-done:
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
		if raw(t, result) != "split response>" || result.Reason != "idle" {
			t.Fatalf("result = %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read did not return")
	}
}

func TestReadDeadlineCancellationAndDisconnect(t *testing.T) {
	h := newHistory("s", 128)
	started := time.Now()
	result, err := h.read(context.Background(), ReadInput{Cursor: "s:0", WaitMS: ptr(5)})
	if err != nil || result.Reason != "deadline" || result.Bytes != 0 || time.Since(started) < 5*time.Millisecond {
		t.Fatalf("timeout = %+v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := h.read(ctx, ReadInput{Cursor: "s:0", WaitMS: ptr(30000)}); done <- err }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	h.append([]byte("last bytes"))
	h.finish("disconnected", errors.New("device removed"))
	result, err = h.read(context.Background(), ReadInput{Cursor: "s:0"})
	if err != nil || result.Reason != "disconnected" || raw(t, result) != "last bytes" || result.Error != "device removed" {
		t.Fatalf("disconnect = %+v, %v", result, err)
	}
}

func TestReadRejectsInvalidCursorsAndLimits(t *testing.T) {
	h := newHistory("s", 128)
	for _, in := range []ReadInput{{Cursor: "old:0"}, {Cursor: "s:1"}, {Cursor: "s:bad"}, {WaitMS: ptr(-1)}, {IdleMS: ptr(5001)}, {MaxBytes: ptr(0)}} {
		if _, err := h.read(context.Background(), in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
}
