package session

import (
	"bytes"
	"fmt"
	"io"
	"time"
)

type lineTimeWriter struct {
	dst       io.Writer
	format    string
	now       func() time.Time
	lineStart bool
}

type receivedOutput struct {
	terminal io.Writer
	log      io.Writer
}

func newReceivedOutput(terminal, log io.Writer, timeFormat string) io.Writer {
	output := io.Writer(&receivedOutput{terminal: terminal, log: log})
	if timeFormat != "" {
		output = newLineTimeWriter(output, timeFormat)
	}
	return output
}

func (w *receivedOutput) Write(data []byte) (int, error) {
	if err := writeFull(w.terminal, data); err != nil {
		return 0, err
	}
	if w.log != nil {
		if err := writeFull(w.log, data); err != nil {
			return 0, fmt.Errorf("write receive log: %w", err)
		}
	}
	return len(data), nil
}

func newLineTimeWriter(dst io.Writer, format string) *lineTimeWriter {
	return &lineTimeWriter{
		dst:       dst,
		format:    format,
		now:       time.Now,
		lineStart: true,
	}
}

func (w *lineTimeWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		if w.lineStart {
			prefix := fmt.Sprintf("[%s] ", w.now().Format(w.format))
			if err := writeFull(w.dst, []byte(prefix)); err != nil {
				return written, err
			}
			w.lineStart = false
		}

		lineEnd := bytes.IndexByte(data, '\n')
		if lineEnd < 0 {
			if err := writeFull(w.dst, data); err != nil {
				return written, err
			}
			return written + len(data), nil
		}

		lineEnd++
		if err := writeFull(w.dst, data[:lineEnd]); err != nil {
			return written, err
		}
		written += lineEnd
		data = data[lineEnd:]
		w.lineStart = true
	}
	return written, nil
}
