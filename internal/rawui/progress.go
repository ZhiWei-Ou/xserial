package rawui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	width := 80
	if columns, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil && columns > 0 {
		width = columns
	}
	line := p.view(event, now, width-1)
	if !localColorsEnabled() {
		line = ansi.Strip(line)
	}
	fmt.Fprintf(w, "\r\x1b[2K%s", line)
	p.open = true
}

// view reserves space for the percentage separately from the bar and drops
// optional fields before shortening the bar below a useful width.
func (p *transferProgress) view(event session.YMODEMProgress, now time.Time, width int) string {
	action := "Sending"
	if event.Direction == "download" {
		action = "Receiving"
	}
	amount := transferSize(float64(event.Written))
	if event.Total > 0 {
		scale, unit := float64(1), "B"
		for _, candidate := range []string{"KiB", "MiB", "GiB"} {
			if float64(event.Total)/scale < 1024 {
				break
			}
			scale *= 1024
			unit = candidate
		}
		precision := 0
		if scale > 1 && float64(event.Total)/scale < 100 {
			precision = 1
		}
		total := fmt.Sprintf("%.*f", precision, float64(event.Total)/scale)
		amount = fmt.Sprintf("%*.*f / %s %s", len(total), precision, float64(event.Written)/scale, total, unit)
	}
	speedText, eta := "         —/s", "ETA     —"
	if elapsed := now.Sub(p.started).Seconds(); elapsed >= 0.5 && event.Written > 0 {
		speed := float64(event.Written) / elapsed
		speedText = fmt.Sprintf("%12s", transferSize(speed)+"/s")
		if event.Total > event.Written {
			remaining := time.Duration(float64(event.Total-event.Written) / speed * float64(time.Second))
			eta = fmt.Sprintf("ETA %5s", remaining.Round(time.Second).String())
		}
	}
	if event.Total > 0 && event.Written >= event.Total {
		eta = "Finishing"
	}
	percent := float64(0)
	percentText := ""
	if event.Total > 0 {
		percent = min(1, max(0, float64(event.Written)/float64(event.Total)))
		percentText = fmt.Sprintf("%3.0f%%", percent*100)
	}
	details := amount + "  " + speedText + "  " + eta
	barWidth := func() int {
		return min(28, width-ansi.StringWidth(action+percentText+details)-6)
	}
	if barWidth() < 12 {
		details = amount + "  " + speedText
	}
	if barWidth() < 12 {
		details = speedText
	}
	if event.Total <= 0 {
		details = amount + "  " + speedText
		if ansi.StringWidth(action+details)+2 > width {
			details = amount
		}
	} else if ansi.StringWidth(action+percentText+details)+4 > width {
		details = ""
	}
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	line := lipgloss.NewStyle().Foreground(lipgloss.Color(localInfoColor)).Render(action) + "  "
	if event.Total > 0 {
		if barWidth() >= 12 {
			bar := progress.New(progress.WithColors(lipgloss.Color("#60A5FA")),
				progress.WithFillCharacters('━', '─'), progress.WithoutPercentage(), progress.WithWidth(barWidth()))
			bar.EmptyColor = lipgloss.Color("#555555")
			line += bar.ViewAs(percent) + "  "
		}
		line += percentText
		if details != "" {
			line += "  "
		}
	}
	line += muted.Render(details)
	return ansi.Truncate(line, max(1, width), "…")
}

func (p *transferProgress) finish(w io.Writer, event session.YMODEMFinished) {
	if p.open {
		fmt.Fprint(w, "\r\x1b[2K")
		p.open = false
	}
	result := formatYMODEMResult(event)
	color := localSuccessColor
	if errors.Is(event.Err, context.Canceled) {
		color = localWarningColor
		result = fmt.Sprintf("– YMODEM %s canceled", event.Direction)
	} else if event.Err != nil {
		color = localErrorColor
		result = fmt.Sprintf("✗ YMODEM %s failed: %v", event.Direction, event.Err)
	} else if !p.started.IsZero() {
		elapsed := time.Since(p.started)
		result += "  ·  " + elapsed.Round(time.Second).String()
		if elapsed > 0 {
			result += "  ·  " + transferSize(float64(event.Bytes)/elapsed.Seconds()) + "/s"
		}
	}
	printLocalLine(w, localColor(result, color))
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
