package transfer

import (
	"bytes"
	"testing"
)

func TestWriteFullHandlesShortWrites(t *testing.T) {
	writer := &shortWriter{limit: 2}
	if err := WriteFull(writer, []byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if writer.String() != "abcdef" {
		t.Fatalf("output = %q", writer.String())
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
