package session

import (
	"bytes"
	"testing"
	"time"
)

func TestLineTimeWriterPrefixesEachReceivedLine(t *testing.T) {
	var log bytes.Buffer
	output := newLineTimeWriter(&log, "15:04:05")
	output.now = func() time.Time {
		return time.Date(2026, time.July, 10, 12, 34, 56, 0, time.Local)
	}

	if _, err := output.Write([]byte("first\r\nsecond")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	want := "[12:34:56] first\r\n[12:34:56] second"
	if got := log.String(); got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}
