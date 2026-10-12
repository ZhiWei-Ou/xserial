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
	event := session.YMODEMProgress{Written: 328 * 1024, Total: 512 * 1024}
	for _, width := range []int{100, 79, 59, 39, 19} {
		line := ansi.Strip(p.view(event, now, width))
		if ansi.StringWidth(line) > width || !strings.Contains(line, "64%") {
			t.Fatalf("width %d: %q", width, line)
		}
		t.Logf("width %d: %s", width, line)
	}
	event.Written = event.Total
	if line := ansi.Strip(p.view(event, now, 100)); strings.Contains(line, "✓") || !strings.Contains(line, "Finishing") {
		t.Fatalf("data complete before protocol result: %q", line)
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
