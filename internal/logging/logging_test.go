package logging

import (
	"bytes"
	"testing"
)

type recordingFormatter struct {
	entries []Entry
}

func (f *recordingFormatter) Format(entry Entry) []byte {
	f.entries = append(f.entries, entry)
	return []byte("formatted")
}

func TestLevelFilteringAndRawOutputUseSeparatePaths(t *testing.T) {
	var output bytes.Buffer
	formatter := &recordingFormatter{}
	logger := New(&output, WithLevel(WarnLevel), WithFormatter(formatter))

	logger.Info("filtered")
	logger.Warn("formatted")
	_, _ = logger.Raw([]byte("raw"))

	if len(formatter.entries) != 1 || formatter.entries[0].Level != WarnLevel {
		t.Fatalf("formatted entries = %#v", formatter.entries)
	}
	if got := output.String(); got != "formattedraw" {
		t.Fatalf("output = %q", got)
	}
}
