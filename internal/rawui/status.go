package rawui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
	"golang.org/x/term"
)

// StatusBar shares one output lock between device data, local messages and the
// bottom row. It only activates when stdout and stderr are the same terminal.
type StatusBar struct {
	Output        io.Writer
	Local         io.Writer
	mu            sync.Mutex
	dst           io.Writer
	size          func() (int, int, error)
	parser        *ansi.Parser
	active        bool
	width, height int
	cfg           session.ConnectionConfig
	state         string
	progress      *session.YMODEMProgress
	started       time.Time
	color         bool
}

func NewStatusBar(output, local *os.File) *StatusBar {
	b := &StatusBar{dst: local, color: localColorsEnabled(), parser: ansi.NewParser()}
	b.parser.SetDataSize(1) // Only the parser state is needed, not OSC/DCS payloads.
	b.Output = &statusWriter{bar: b, file: output}
	b.Local = &statusWriter{bar: b, file: local}
	outInfo, outErr := output.Stat()
	localInfo, localErr := local.Stat()
	if outErr == nil && localErr == nil && os.SameFile(outInfo, localInfo) &&
		term.IsTerminal(int(output.Fd())) && term.IsTerminal(int(local.Fd())) && !strings.EqualFold(os.Getenv("TERM"), "dumb") {
		b.size = func() (int, int, error) { return term.GetSize(int(local.Fd())) }
	}
	return b
}

type statusWriter struct {
	bar  *StatusBar
	file *os.File
}

func (w *statusWriter) Fd() uintptr { return w.file.Fd() }
func (w *statusWriter) Write(data []byte) (int, error) {
	b := w.bar
	b.mu.Lock()
	defer b.mu.Unlock()
	n, err := w.file.Write(data)
	if b.active {
		for _, c := range data[:n] {
			b.parser.Advance(c)
		}
		if err == nil {
			err = b.drawLocked()
		}
	}
	return n, err
}

func (b *StatusBar) start(cfg session.ConnectionConfig) error {
	if b == nil || b.size == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cfg, b.state = cfg, "Connected"
	return b.resizeLocked()
}

func (b *StatusBar) enabled() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active
}

// refresh also detects size changes on platforms without SIGWINCH.
func (b *StatusBar) refresh() (bool, error) {
	if b == nil || b.size == nil {
		return false, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	oldWidth, oldHeight := b.width, b.height
	if err := b.resizeLocked(); err != nil {
		return false, err
	}
	return oldWidth != b.width || oldHeight != b.height, b.drawLocked()
}

func (b *StatusBar) resizeLocked() error {
	if b.parser.State() != parser.GroundState {
		return nil
	}
	width, height, err := b.size()
	if err != nil {
		return err
	}
	if width == b.width && height == b.height {
		return nil
	}
	var control strings.Builder
	if b.active {
		fmt.Fprintf(&control, "\x1b[%d;1H\x1b[0m\x1b[2K\x1b[r", min(b.height, height))
	}
	b.width, b.height = width, height
	b.active = width >= 12 && height >= 3
	if b.active {
		// Reserve a fresh bottom row without switching to the alternate screen.
		if control.Len() == 0 {
			fmt.Fprintf(&control, "\x1b[%d;1H\r\n", height)
		}
		fmt.Fprintf(&control, "\x1b[1;%dr\x1b[%d;1H", height-1, height-1)
	}
	if _, err := io.WriteString(b.dst, control.String()); err != nil {
		return err
	}
	return b.drawLocked()
}

func (b *StatusBar) close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.active {
		return nil
	}
	b.active = false
	// Cancel a possible unfinished device escape before restoring the terminal.
	_, err := fmt.Fprintf(b.dst, "\x18\x1b[0m\x1b[r\x1b[%d;1H\x1b[2K", b.height)
	return err
}

func (b *StatusBar) update(state string, progress *session.YMODEMProgress, started time.Time) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if state != "" {
		b.state = state
	}
	b.progress, b.started = progress, started
}

func (b *StatusBar) drawLocked() error {
	if !b.active || b.parser.State() != parser.GroundState {
		return nil
	}
	// Save/restore both cursor and attributes; never write into the last column.
	_, err := fmt.Fprintf(b.dst, "\x1b7\x1b[%d;1H\x1b[0m\x1b[2K%s\x1b8", b.height, b.view(time.Now()))
	return err
}

func (b *StatusBar) view(now time.Time) string {
	width := max(1, b.width-1)
	port := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, b.cfg.PortName)
	parity := "N"
	if b.cfg.Parity != "" {
		parity = strings.ToUpper(b.cfg.Parity[:1])
	}
	settings := fmt.Sprintf(" · %d · %d%s%s", b.cfg.BaudRate, b.cfg.DataBits, parity, b.cfg.StopBits)
	port = filepath.Base(port)
	right, compact := "", ""
	if p := b.progress; p != nil {
		right = "↑"
		if p.Direction == "download" {
			right = "↓"
		}
		if p.Total > 0 {
			right += fmt.Sprintf(" %.0f%%", min(100, max(0, 100*float64(p.Written)/float64(p.Total))))
		}
		compact = right
		if elapsed := now.Sub(b.started).Seconds(); !b.started.IsZero() && elapsed >= 0.5 {
			right += " · " + transferSize(float64(p.Written)/elapsed) + "/s"
		}
	}
	if b.state != "Connected" {
		right, compact = b.state, b.state
	}
	available := width
	if right != "" {
		available -= ansi.StringWidth(right) + 3
		if available-ansi.StringWidth(settings) < 8 {
			right = compact
			available = width - ansi.StringWidth(right) - 3
		}
		if available-ansi.StringWidth(settings) < 4 {
			right = ""
			available = width
		}
	}
	port = ansi.Truncate(port, max(1, available-ansi.StringWidth(settings)), "…")
	left := ansi.Truncate(port+settings, available, "…")
	gap := ""
	if right != "" {
		gap = strings.Repeat(" ", max(3, width-ansi.StringWidth(left)-ansi.StringWidth(right)))
	}
	if !b.color {
		return left + gap + right
	}
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#64748B"))
	label := lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))
	left = ansi.Truncate(label.Render(port)+muted.Render(settings), available, "…")
	rightStyle := muted
	if b.state != "Connected" {
		rightStyle = rightStyle.Foreground(lipgloss.Color(localWarningColor))
	}
	return left + gap + rightStyle.Render(right)
}
