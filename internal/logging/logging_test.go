package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerFormatsEventsAndPreservesRawLocalOutput(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output)
	logger.Warn("session.warning", "detail", "first\nsecond")
	logger.Error("session.failed", "error", "device disconnected")
	raw := []byte("\r\x1b[2Kprogress")
	if n, err := logger.Write(raw); err != nil || n != len(raw) {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	got := output.String()
	if !strings.Contains(got, "[WARN] session.warning detail=\"first\\nsecond\"\r\n") ||
		!strings.Contains(got, "[ERROR] session.failed error=\"device disconnected\"\r\n") ||
		strings.Count(got, "\n") != 2 || !bytes.HasSuffix(output.Bytes(), raw) {
		t.Fatalf("output = %q", got)
	}
}
