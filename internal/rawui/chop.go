package rawui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ZhiWei-Ou/xserial/internal/transfer"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
	"golang.org/x/term"
)

// Only terminal display is clipped. Pipes, logs and recordings retain all bytes.
func chopTerminalOutput(dst io.Writer) io.Writer {
	file, ok := dst.(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(int(file.Fd())) || strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return dst
	}
	return newChopWriter(dst, func() (int, error) {
		width, _, err := term.GetSize(int(file.Fd()))
		return width, err
	})
}

// chopWriter processes a stream, not individual receive chunks. Once text has
// been discarded, the remainder of that line stays hidden even after widening.
// It retains only parser state; there is no line history to restore on resize.
type chopWriter struct {
	dst             io.Writer
	size            func() (int, error)
	parser          *ansi.Parser
	width, col      int
	savedCol        int
	clipped         bool
	lastVisible     bool
	passthrough     bool
	positionPending bool
	out             []byte
}

func newChopWriter(dst io.Writer, size func() (int, error)) *chopWriter {
	w := &chopWriter{dst: dst, size: size, parser: ansi.NewParser()}
	w.parser.SetDataSize(1) // Escape payloads pass through without being retained.
	w.parser.SetHandler(ansi.Handler{
		Print: w.print, Execute: w.control, HandleCsi: w.csi,
		HandleEsc: func(cmd ansi.Cmd) {
			switch cmd {
			case '7':
				w.savedCol = min(w.col, w.width-1)
			case '8':
				w.move(w.savedCol)
			case 'E': // NEL
				w.move(0)
			}
		},
	})
	return w
}

func (w *chopWriter) Write(data []byte) (int, error) {
	width, err := w.size()
	if err != nil {
		return 0, fmt.Errorf("read terminal width: %w", err)
	}
	if width <= 0 {
		return 0, fmt.Errorf("invalid terminal width: %d", width)
	}
	if w.width != 0 && width != w.width && w.col > 0 && !w.clipped {
		w.positionPending = true
	}
	w.width = width
	w.out = w.out[:0]
	for _, b := range data {
		state := w.parser.State()
		// UTF-8 must be complete before deciding whether its cells fit. Escape
		// sequences are forwarded byte-for-byte, including across Write calls.
		w.passthrough = state != parser.Utf8State && !(state == parser.GroundState && b >= 0xc2 && b <= 0xf4)
		w.parser.Advance(b)
		if w.passthrough {
			w.out = append(w.out, b)
		}
	}
	if err := transfer.WriteFull(w.dst, w.out); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *chopWriter) print(r rune) {
	w.passthrough = false
	cells := ansi.StringWidth(string(r))
	if cells == 0 {
		if w.lastVisible {
			w.out = append(w.out, string(r)...)
		}
		return
	}
	w.lastVisible = !w.clipped && w.col+cells <= w.width
	if w.lastVisible {
		if w.positionPending {
			// Resize clears the terminal's pending wrap at the old margin.
			w.out = fmt.Appendf(w.out, "\x1b[%dG", w.col+1)
			w.positionPending = false
		}
		w.out = append(w.out, string(r)...)
		w.col += cells
	} else {
		w.clipped = true
	}
}

func (w *chopWriter) control(b byte) {
	w.passthrough = false
	switch b {
	case '\r':
		w.move(0)
	case '\n', '\v', '\f':
		// Raw mode does not translate LF. Begin the next logical line at column 0.
		if w.col != 0 || w.clipped {
			w.out = append(w.out, '\r')
		}
		w.move(0)
	case '\t':
		count := 8 - w.col%8
		for range count {
			w.print(' ')
		}
		return
	case '\b':
		if w.clipped {
			return
		}
		w.move(max(0, w.col-1))
		// Absolute positioning also clears pending wrap at the right margin.
		w.out = fmt.Appendf(w.out, "\x1b[%dG", w.col+1)
		return
	}
	w.out = append(w.out, b)
}

func (w *chopWriter) move(col int) {
	w.col = max(0, min(col, w.width-1))
	w.clipped, w.lastVisible, w.positionPending = false, false, false
}

func (w *chopWriter) csi(cmd ansi.Cmd, params ansi.Params) {
	value, _, _ := params.Param(0, 1)
	value = max(1, value)
	switch cmd {
	case 'G', '`':
		w.move(value - 1)
	case 'H', 'f':
		column, _, _ := params.Param(1, 1)
		w.move(max(1, column) - 1)
	case 'C', 'a':
		w.move(min(w.col, w.width-1) + value)
	case 'D':
		w.move(min(w.col, w.width-1) - value)
	case 'E', 'F':
		w.move(0)
	case 's':
		w.savedCol = min(w.col, w.width-1)
	case 'u':
		w.move(w.savedCol)
	}
}

// Local UI messages end on a new line independently of device line endings.
func (w *chopWriter) resetLine() {
	if w != nil {
		w.move(0)
	}
}
