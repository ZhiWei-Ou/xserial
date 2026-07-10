package logging

import (
	"bytes"
	"errors"
	"testing"
)

func TestLoggerWritesDistinctStructuredEntry(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output)

	logger.Info("session.connected", "port", "/dev/ttyUSB0", "baud", 115200)

	want := "[[ xserial | INFO | session.connected ]] port=\"/dev/ttyUSB0\" baud=115200\r\n"
	if got := output.String(); got != want {
		t.Fatalf("Info() output = %q, want %q", got, want)
	}
}

func TestLoggerFormatsErrorAndMissingValue(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output)

	logger.Error("application.failed", "error", errors.New("port closed"), "detail")

	want := "[[ xserial | ERROR | application.failed ]] error=\"port closed\" detail=\"<missing>\"\r\n"
	if got := output.String(); got != want {
		t.Fatalf("Error() output = %q, want %q", got, want)
	}
}
