package session

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/transfer"
)

type lineTimeWriter struct {
	dst       io.Writer
	format    string
	now       func() time.Time
	lineStart bool
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
			if err := transfer.WriteFull(w.dst, []byte(prefix)); err != nil {
				return written, err
			}
			w.lineStart = false
		}

		lineEnd := bytes.IndexByte(data, '\n')
		if lineEnd < 0 {
			if err := transfer.WriteFull(w.dst, data); err != nil {
				return written, err
			}
			return written + len(data), nil
		}

		lineEnd++
		if err := transfer.WriteFull(w.dst, data[:lineEnd]); err != nil {
			return written, err
		}
		written += lineEnd
		data = data[lineEnd:]
		w.lineStart = true
	}
	return written, nil
}
