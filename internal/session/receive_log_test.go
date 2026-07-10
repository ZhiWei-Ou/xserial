package session

import (
	"bytes"
	"testing"
	"time"
)

func TestReceiveLogAddsTimeAtEachLineStart(t *testing.T) {
	var output bytes.Buffer
	writer := newLineTimeWriter(&output, "15:04:05.000")
	writer.now = func() time.Time {
		return time.Date(2026, time.July, 10, 12, 34, 56, 789000000, time.Local)
	}

	for _, data := range []string{"first\r", "\nsecond\n\n", "third"} {
		if _, err := writer.Write([]byte(data)); err != nil {
			t.Fatalf("Write(%q) error = %v", data, err)
		}
	}

	want := "[12:34:56.789] first\r\n" +
		"[12:34:56.789] second\n" +
		"[12:34:56.789] \n" +
		"[12:34:56.789] third"
	if got := output.String(); got != want {
		t.Fatalf("receive log = %q, want %q", got, want)
	}
}

func TestReceivedOutputAddsSameTimeToTerminalAndLog(t *testing.T) {
	var terminal bytes.Buffer
	var log bytes.Buffer
	output := newReceivedOutput(&terminal, &log, "15:04:05").(*lineTimeWriter)
	output.now = func() time.Time {
		return time.Date(2026, time.July, 10, 12, 34, 56, 0, time.Local)
	}

	if _, err := output.Write([]byte("device output\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	want := "[12:34:56] device output\n"
	if got := terminal.String(); got != want {
		t.Fatalf("terminal = %q, want %q", got, want)
	}
	if got := log.String(); got != want {
		t.Fatalf("log = %q, want %q", got, want)
	}
}

func TestReceivedOutputAddsTimeToTerminalWithoutLog(t *testing.T) {
	var terminal bytes.Buffer
	output := newReceivedOutput(&terminal, nil, "15:04:05").(*lineTimeWriter)
	output.now = func() time.Time {
		return time.Date(2026, time.July, 10, 12, 34, 56, 0, time.Local)
	}

	if _, err := output.Write([]byte("device output")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	want := "[12:34:56] device output"
	if got := terminal.String(); got != want {
		t.Fatalf("terminal = %q, want %q", got, want)
	}
}
