package linetime

import (
	"bytes"
	"testing"
	"time"
)

func TestWriterPrefixesLinesAcrossChunks(t *testing.T) {
	var output bytes.Buffer
	w := NewWriter(&output, "15:04:05")
	w.now = func() time.Time {
		return time.Date(2026, time.July, 10, 12, 34, 56, 0, time.Local)
	}

	for _, chunk := range [][]byte{[]byte("first\r"), []byte("\nsecond\n"), []byte("third")} {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	want := "[12:34:56] first\r\n[12:34:56] second\n[12:34:56] third"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
