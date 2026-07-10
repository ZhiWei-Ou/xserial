package transfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCopyRawHandlesShortWrites(t *testing.T) {
	writer := &shortWriter{limit: 2}
	var progress int64
	n, err := CopyRaw(context.Background(), strings.NewReader("abcdef"), writer, 4, 6, func(written, _ int64) {
		progress = written
	})
	if err != nil || n != 6 || writer.String() != "abcdef" || progress != 6 {
		t.Fatalf("bytes=%d output=%q progress=%d error=%v", n, writer.String(), progress, err)
	}
}

func TestCopyRawRejectsInvalidChunkSize(t *testing.T) {
	_, err := CopyRaw(context.Background(), strings.NewReader("x"), io.Discard, 0, 1, nil)
	if err == nil {
		t.Fatal("CopyRaw() error = nil")
	}
}

func TestCopyRawRespondsToCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := CopyRaw(ctx, strings.NewReader("x"), io.Discard, 1, 1, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CopyRaw() error = %v", err)
	}
}

type shortWriter struct {
	bytes.Buffer
	limit int
}

func (w *shortWriter) Write(data []byte) (int, error) {
	if len(data) > w.limit {
		data = data[:w.limit]
	}
	return w.Buffer.Write(data)
}
