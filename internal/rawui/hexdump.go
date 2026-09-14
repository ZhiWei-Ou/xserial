package rawui

import (
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/ZhiWei-Ou/xserial/internal/transfer"
)

// hexWriter emits each received chunk immediately, including its final short
// row. Offsets count displayed device bytes across chunks, not formatted bytes.
type hexWriter struct {
	dst    io.Writer
	offset uint64
}

func (w *hexWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		n := min(len(data), 16)
		// Dump supplies the canonical byte/ASCII columns; use our session offset
		// and CRLF because the terminal runs in raw mode.
		row := fmt.Sprintf("%08x%s\r\n", w.offset, strings.TrimSuffix(hex.Dump(data[:n])[8:], "\n"))
		if err := transfer.WriteFull(w.dst, []byte(row)); err != nil {
			return written, err
		}
		w.offset += uint64(n)
		written += n
		data = data[n:]
	}
	return written, nil
}
