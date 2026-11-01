package rawui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/charmbracelet/x/ansi"
)

func TestTransferProgressLayout(t *testing.T) {
	now := time.Now()
	p := transferProgress{started: now.Add(-32 * time.Second)}
	event := session.YMODEMProgress{Direction: "upload", Path: "/tmp/firmware.bin", Written: 328 * 1024, Total: 512 * 1024}
	for _, width := range []int{100, 79, 59, 39, 19} {
		line := p.view(event, now, width, false)
		if ansi.StringWidth(line) > width || !strings.Contains(line, "64%") {
			t.Fatalf("width %d: %q", width, line)
		}
		if width >= 39 && !strings.Contains(line, "█") {
			t.Fatalf("width %d dropped the bar: %q", width, line)
		}
		t.Logf("width %d: %s", width, line)
	}
	full := ansi.Strip(p.view(event, now, 100, true))
	for _, want := range []string{"Sending firmware.bin", "328 / 512 KiB", "10.2 KiB/s", "18s left"} {
		if !strings.Contains(full, want) {
			t.Fatalf("missing %q: %q", want, full)
		}
	}
	event.Written = event.Total
	if line := ansi.Strip(p.view(event, now, 100, true)); strings.Contains(line, "✓") || !strings.Contains(line, "finishing") {
		t.Fatalf("data complete before protocol result: %q", line)
	}
}

func TestTransferProgressBarLengthIsStable(t *testing.T) {
	now := time.Now()
	event := session.YMODEMProgress{Direction: "upload", Path: "fw.bin", Written: 1024, Total: 512 * 1024}
	barLen := func(started time.Time) int {
		p := transferProgress{started: started}
		line := p.view(event, now, 80, false)
		return strings.Count(line, "█") + strings.Count(line, "▌") + strings.Count(line, "░")
	}
	// No speed yet versus a multi-hour ETA must not change the bar.
	if a, b := barLen(now), barLen(now.Add(-10*time.Second)); a != b {
		t.Fatalf("bar length changed between redraws: %d vs %d", a, b)
	}
}

func TestTransferProgressUnknownTotal(t *testing.T) {
	now := time.Now()
	p := transferProgress{started: now.Add(-2 * time.Second)}
	event := session.YMODEMProgress{Direction: "download", Written: 4096}
	line := ansi.Strip(p.view(event, now, 80, false))
	if line != "Receiving  4.0 KiB · 2.0 KiB/s" {
		t.Fatalf("line: %q", line)
	}
}

func TestProgressBarHalfCells(t *testing.T) {
	if got := progressBar(0.25, 10, false, ""); got != "██▌░░░░░░░" {
		t.Fatalf("bar: %q", got)
	}
	if got := progressBar(1, 4, false, ""); got != "████" {
		t.Fatalf("bar: %q", got)
	}
	if got := ansi.Strip(progressBar(0.25, 10, true, "#60A5FA")); got != "██▌███████" {
		t.Fatalf("color bar: %q", got)
	}
}

func TestTransferProgressFailureReplacesFullBar(t *testing.T) {
	p := transferProgress{open: true}
	var out bytes.Buffer
	p.finish(&out, session.YMODEMFinished{Direction: "upload", Err: errors.New("final ACK timeout")})
	if got := out.String(); !strings.HasPrefix(got, "\r\x1b[2K✗") || !strings.Contains(got, "final ACK timeout") || strings.Contains(got, "✓") || p.open {
		t.Fatalf("finish output: %q, open: %v", got, p.open)
	}
}
