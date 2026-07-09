package session

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadRawFileSendsFileContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "firmware.bin")
	if err := os.WriteFile(path, []byte("raw file content"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var serial bytes.Buffer
	n, err := uploadRawFile(context.Background(), path, &serial)
	if err != nil {
		t.Fatalf("uploadRawFile() error = %v", err)
	}
	if n != int64(len("raw file content")) {
		t.Fatalf("uploadRawFile() bytes = %d, want %d", n, len("raw file content"))
	}
	if got := serial.String(); got != "raw file content" {
		t.Fatalf("serial output = %q, want %q", got, "raw file content")
	}
}

func TestUploadRawFileRejectsDirectory(t *testing.T) {
	var serial bytes.Buffer

	_, err := uploadRawFile(context.Background(), t.TempDir(), &serial)
	if err == nil {
		t.Fatalf("uploadRawFile() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("uploadRawFile() error = %v, want not a regular file", err)
	}
	if serial.Len() != 0 {
		t.Fatalf("serial output = %q, want empty", serial.String())
	}
}

func TestSessionUploadsFileWithoutWritingPathToSerial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "upload.bin")
	if err := os.WriteFile(path, []byte("abc123"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	port := &fakePort{
		read:      bytes.NewBuffer(nil),
		blockRead: true,
		readDone:  make(chan struct{}),
	}
	terminal := &fakeTerminal{}
	input := string([]byte{DefaultPrefixKey, 'u'}) + path + "\r" + string([]byte{DefaultPrefixKey, 'q'})
	session := New(Config{
		Port:     port,
		Terminal: terminal,
		Stdin:    bytes.NewBufferString(input),
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	})

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if got := string(port.Written()); got != "abc123" {
		t.Fatalf("serial write = %q, want uploaded file content only", got)
	}
}

func TestCopyRawRejectsInvalidChunkSize(t *testing.T) {
	_, err := copyRaw(context.Background(), strings.NewReader("x"), io.Discard, 0)
	if err == nil {
		t.Fatalf("copyRaw() error = nil, want error")
	}
	if err.Error() != "chunk size must be positive" {
		t.Fatalf("copyRaw() error = %v, want chunk size must be positive", err)
	}
}

func TestCopyRawHandlesShortWrites(t *testing.T) {
	writer := &shortWriter{}

	n, err := copyRaw(context.Background(), strings.NewReader("abcdef"), writer, 4)
	if err != nil {
		t.Fatalf("copyRaw() error = %v", err)
	}
	if n != 6 {
		t.Fatalf("copyRaw() bytes = %d, want 6", n)
	}
	if got := writer.String(); got != "abcdef" {
		t.Fatalf("writer output = %q, want abcdef", got)
	}
}

type shortWriter struct {
	bytes.Buffer
}

func (w *shortWriter) Write(buf []byte) (int, error) {
	if len(buf) > 2 {
		buf = buf[:2]
	}
	return w.Buffer.Write(buf)
}
