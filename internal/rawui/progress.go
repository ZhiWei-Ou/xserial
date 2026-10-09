package rawui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

type transferProgress struct {
	started  time.Time
	lastDraw time.Time
	open     bool
}

func (p *transferProgress) render(w io.Writer, event session.YMODEMProgress) {
	now := time.Now()
	if p.started.IsZero() {
		p.started = now
	}
	if p.open && now.Sub(p.lastDraw) < 80*time.Millisecond && event.Written != event.Total {
		return
	}
	p.lastDraw = now
	action := "Sending"
	if event.Direction == "download" {
		action = "Receiving"
	}
	amount := transferSize(float64(event.Written))
	if event.Total > 0 {
		amount += " / " + transferSize(float64(event.Total))
	}
	details := amount
	if elapsed := now.Sub(p.started).Seconds(); elapsed >= 0.5 && event.Written > 0 {
		speed := float64(event.Written) / elapsed
		details += "  " + transferSize(speed) + "/s"
		if event.Total > event.Written {
			remaining := time.Duration(float64(event.Total-event.Written)/speed) * time.Second
			details += "  ETA " + remaining.String()
		}
	}
	width := 80
	if columns, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && columns > 0 {
		width = columns
	}
	barWidth := min(32, width-ansi.StringWidth(action+details)-5)
	if barWidth < 12 {
		details = amount
		barWidth = min(32, width-ansi.StringWidth(action+details)-5)
	}
	line := action + "  " + details
	if event.Total > 0 {
		percent := min(1, max(0, float64(event.Written)/float64(event.Total)))
		if barWidth >= 12 {
			bar := progress.New(progress.WithColors(lipgloss.Color("#FDBA74"), lipgloss.Color("#EA580C")), progress.WithWidth(barWidth))
			line = action + "  " + bar.ViewAs(percent) + "  " + details
		} else {
			line = fmt.Sprintf("%s  %.0f%%  %s", action, percent*100, details)
		}
	}
	if os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb") || !term.IsTerminal(int(os.Stderr.Fd())) {
		line = ansi.Strip(line)
	}
	fmt.Fprintf(w, "\r\x1b[2K%s", ansi.Truncate(line, max(1, width-1), "…"))
	p.open = true
}

func (p *transferProgress) endLine(w io.Writer) {
	if p.open {
		fmt.Fprint(w, "\r\n")
		p.open = false
	}
}

func transferSize(n float64) string {
	for _, unit := range []string{"B", "KiB", "MiB", "GiB"} {
		if n < 1024 || unit == "GiB" {
			return fmt.Sprintf("%.1f %s", n, unit)
		}
		n /= 1024
	}
	return ""
}
