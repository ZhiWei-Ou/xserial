package rawui

import (
	"fmt"
	"io"
	"math/rand/v2"
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
	mode          inputState
	tipIndex      int
	tipChanged    time.Time
	color         bool
	traffic       func() (tx, rx uint64)
	samples       []trafficSample
	tx, rx        uint64
	rate          float64
}

func NewStatusBar(output, local *os.File, traffic func() (tx, rx uint64)) *StatusBar {
	b := &StatusBar{dst: local, traffic: traffic, color: localColorsEnabled(), parser: ansi.NewParser()}
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
	b.sampleTraffic(time.Now())
	return b.resizeLocked()
}

// refresh also detects size changes on platforms without SIGWINCH.
func (b *StatusBar) refresh() (bool, error) {
	if b == nil || b.size == nil {
		return false, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sampleTraffic(time.Now())
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

func (b *StatusBar) update(state string, mode inputState) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if state != "" {
		b.state = state
	}
	b.mode = mode
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
	settings := fmt.Sprintf("/%d/%d%s%s", b.cfg.BaudRate, b.cfg.DataBits, parity, b.cfg.StopBits)
	port = filepath.Base(port)
	right, compact := b.tip(now)
	right, compact = "Tip: "+right, "Tip: "+compact
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
	if b.traffic != nil {
		runtime := fmt.Sprintf(" · TX %s | RX %s | RATE %s/s",
			transferSize(float64(b.tx)), transferSize(float64(b.rx)), transferSize(b.rate))
		if ansi.StringWidth(port+settings+runtime) <= available {
			settings += runtime
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
	rightStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	if b.state != "Connected" {
		rightStyle = rightStyle.Foreground(lipgloss.Color(localWarningColor))
	}
	return left + gap + rightStyle.Render(right)
}

var normalTips = []string{
	"Ctrl-P h: help",
	"Ctrl-P i: port info",
	"Ctrl-P Ctrl-U: upload file",
	"Ctrl-P Ctrl-D: receive file",
	"Ctrl-P q: quit",
}

// Tips change slowly and never repeat immediately, regardless of output rate.
func (b *StatusBar) tip(now time.Time) (string, string) {
	switch b.mode {
	case inputYMODEMUploadPath:
		return "Tab: complete · Ctrl-C: clear · Enter: send · Esc: cancel", "Tab · Ctrl-C clear · Enter send · Esc cancel"
	case inputYMODEMTransfer:
		return "Esc: cancel transfer", "Esc: cancel"
	case inputCancellingTransfer:
		return "Canceling transfer…", "Canceling…"
	}
	if b.tipChanged.IsZero() {
		b.tipIndex = rand.IntN(len(normalTips))
		b.tipChanged = now
	} else if now.Sub(b.tipChanged) >= 8*time.Second {
		b.tipIndex = (b.tipIndex + 1 + rand.IntN(len(normalTips)-1)) % len(normalTips)
		b.tipChanged = now
	}
	return normalTips[b.tipIndex], normalTips[b.tipIndex]
}

type trafficSample struct {
	at    time.Time
	total uint64
}

// sampleTraffic runs on the 200 ms refresh timer, independently of terminal
// writes. Retain the sample before the cutoff to interpolate a two-second window.
func (b *StatusBar) sampleTraffic(now time.Time) {
	if b.traffic == nil {
		return
	}
	b.tx, b.rx = b.traffic()
	b.samples = append(b.samples, trafficSample{at: now, total: b.tx + b.rx})
	cutoff := now.Add(-2 * time.Second)
	for len(b.samples) > 1 && !b.samples[1].at.After(cutoff) {
		b.samples = b.samples[1:]
	}
	first := b.samples[0]
	start := first.at
	delta := float64(b.tx + b.rx - first.total)
	if start.Before(cutoff) && len(b.samples) > 1 {
		next := b.samples[1]
		delta -= float64(next.total-first.total) * cutoff.Sub(start).Seconds() / next.at.Sub(start).Seconds()
		start = cutoff
	}
	b.rate = 0
	if elapsed := now.Sub(start).Seconds(); elapsed > 0 {
		b.rate = max(0, delta/elapsed)
	}
}
