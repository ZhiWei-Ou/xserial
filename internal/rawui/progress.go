package rawui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	fmt.Fprintf(w, "\r\x1b[2K%s", p.view(event, now, width-1, localColorsEnabled()))
	p.open = true
}

const (
	progressBarMax = 32
	progressBarMin = 10
	// Reserved widths keep the bar length steady while speed and ETA text
	// change length between redraws.
	progressSpeedWidth = len("1023.9 KiB/s")
	progressETAWidth   = len("99h59m left")
	progressNameMax    = 24
)

// view lays out "Sending name  ████▌░░░  64%  328 / 512 KiB · 10.2 KiB/s · 18s left".
// Optional fields are dropped (speed, ETA, name, amount) before the bar
// shrinks below a useful width; the layout depends only on the total and
// the terminal width, so the bar does not jitter between redraws.
func (p *transferProgress) view(event session.YMODEMProgress, now time.Time, width int, color bool) string {
	width = max(1, width)
	action := "Sending"
	if event.Direction == "download" {
		action = "Receiving"
	}
	name := ""
	if event.Path != "" {
		name = ansi.Truncate(filepath.Base(event.Path), progressNameMax, "…")
	}

	amount, amountWidth := transferSize(float64(event.Written)), 0
	if event.Total > 0 {
		amount, amountWidth = transferAmount(event.Written, event.Total)
	}
	speed, eta := "", ""
	if elapsed := now.Sub(p.started).Seconds(); elapsed >= 0.5 && event.Written > 0 {
		rate := float64(event.Written) / elapsed
		speed = transferSize(rate) + "/s"
		if event.Total > event.Written {
			remaining := time.Duration(float64(event.Total-event.Written) / rate * float64(time.Second))
			eta = transferETA(remaining) + " left"
		}
	}
	if event.Total > 0 && event.Written >= event.Total {
		eta = "finishing…"
	}

	type field struct {
		text    string
		reserve int
	}
	// Field order is display order; drop order is speed, ETA, name, amount.
	amountField, speedField, etaField := &field{amount, amountWidth}, &field{speed, progressSpeedWidth}, &field{eta, progressETAWidth}
	if event.Total <= 0 {
		etaField.reserve = 0
	}
	details := []*field{amountField, speedField, etaField}
	showName := name != ""
	fixedWidth := func() int {
		n := len(action)
		if showName {
			n += 1 + ansi.StringWidth(name)
		}
		if event.Total > 0 {
			n += 2 + 1 + 4 // gaps around the bar, then the percentage
		}
		gap := 2
		for _, d := range details {
			if w := max(d.reserve, ansi.StringWidth(d.text)); w > 0 {
				n += gap + w
				gap = 3
			}
		}
		return n
	}
	fits := func() bool {
		if event.Total > 0 {
			return width-fixedWidth() >= progressBarMin
		}
		return fixedWidth() <= width
	}
	for _, drop := range []func(){
		func() { *speedField = field{} },
		func() { *etaField = field{} },
		func() { showName = false },
		func() { *amountField = field{} },
	} {
		if fits() {
			break
		}
		drop()
	}

	style := func(text, c string) string {
		if !color || text == "" {
			return text
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(text)
	}
	line := style(action, localInfoColor)
	if showName {
		line += " " + style(name, "#E5E7EB")
	}
	if event.Total > 0 {
		percent := min(1, max(0, float64(event.Written)/float64(event.Total)))
		if barWidth := min(progressBarMax, width-fixedWidth()); barWidth >= progressBarMin {
			fill := "#60A5FA"
			if percent >= 1 {
				fill = localSuccessColor
			}
			line += "  " + progressBar(percent, barWidth, color, fill)
		}
		line += fmt.Sprintf(" %3.0f%%", percent*100)
	}
	parts := make([]string, 0, len(details))
	for _, d := range details {
		if d.text != "" {
			parts = append(parts, d.text)
		}
	}
	if len(parts) > 0 {
		line += "  " + style(strings.Join(parts, " · "), "#808080")
	}
	return ansi.Truncate(line, width, "…")
}

// progressBar draws a solid bar with half-cell resolution. With color the
// empty track is a dim solid block, so the bar reads as one thick strip;
// without color the track uses a light shade.
func progressBar(percent float64, width int, color bool, fill string) string {
	halves := int(percent*float64(width*2) + 0.5)
	full, half := halves/2, halves%2
	empty := width - full - half
	if !color {
		return strings.Repeat("█", full) + strings.Repeat("▌", half) + strings.Repeat("░", empty)
	}
	const track = "#3F3F46"
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(fill)).Render(strings.Repeat("█", full))
	if half == 1 {
		// The right half of ▌ shows the background, so paint it as track.
		bar += lipgloss.NewStyle().Foreground(lipgloss.Color(fill)).Background(lipgloss.Color(track)).Render("▌")
	}
	return bar + lipgloss.NewStyle().Foreground(lipgloss.Color(track)).Render(strings.Repeat("█", empty))
}

// transferETA keeps the estimate to two units: "18s", "8m50s", "1h25m".
func transferETA(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour {
		d = d.Round(time.Minute)
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return d.String()
}

// transferAmount formats "written / total unit" in the total's unit, padding
// written to the total's width; it also returns that fixed width.
func transferAmount(written, total int64) (string, int) {
	scale, unit := float64(1), "B"
	for _, candidate := range []string{"KiB", "MiB", "GiB"} {
		if float64(total)/scale < 1024 {
			break
		}
		scale *= 1024
		unit = candidate
	}
	precision := 0
	if scale > 1 && float64(total)/scale < 100 {
		precision = 1
	}
	totalText := fmt.Sprintf("%.*f", precision, float64(total)/scale)
	text := fmt.Sprintf("%*.*f / %s %s", len(totalText), precision, float64(written)/scale, totalText, unit)
	return text, ansi.StringWidth(text)
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
	if n < 1024 {
		return fmt.Sprintf("%.0f B", n)
	}
	n /= 1024
	for _, unit := range []string{"KiB", "MiB", "GiB"} {
		if n < 1024 || unit == "GiB" {
			return fmt.Sprintf("%.1f %s", n, unit)
		}
		n /= 1024
	}
	return ""
}
