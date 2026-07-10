package tui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ZhiWei-Ou/xserial/internal/session"
	"github.com/charmbracelet/x/ansi"
)

type fakeEndpoint struct {
	events   chan session.Event
	sent     [][]byte
	upload   string
	canceled bool
	quit     bool
	err      error
}

func newFakeEndpoint() *fakeEndpoint                 { return &fakeEndpoint{events: make(chan session.Event, 8)} }
func (e *fakeEndpoint) Events() <-chan session.Event { return e.events }
func (e *fakeEndpoint) Send(_ context.Context, data []byte) error {
	e.sent = append(e.sent, append([]byte(nil), data...))
	return e.err
}
func (e *fakeEndpoint) StartUpload(_ context.Context, path string) error {
	e.upload = path
	return e.err
}
func (e *fakeEndpoint) CancelUpload() { e.canceled = true }
func (e *fakeEndpoint) Quit()         { e.quit = true }

func TestTextModeSendsLineAndHexModeSendsExactBytes(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	enterText(t, m, "ver")
	_, command := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	msg := command()
	m.Update(msg)
	if got := endpoint.sent[0]; !bytes.Equal(got, []byte("ver\r")) {
		t.Fatalf("text send = %v", got)
	}

	m.switchMode()
	enterText(t, m, "AA 01 ff")
	_, command = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	msg = command()
	m.Update(msg)
	if got := endpoint.sent[1]; !bytes.Equal(got, []byte{0xaa, 0x01, 0xff}) {
		t.Fatalf("hex send = %v", got)
	}
}

func TestHexInputRequiresTwoDigitGroups(t *testing.T) {
	for _, input := range []string{"A", "0xAA", "AABB", "GG"} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseHexInput(input); err == nil {
				t.Fatalf("parseHexInput(%q) error = nil", input)
			}
		})
	}
}

func TestHexReceiveUsesClassicDumpAndModeOnlyAffectsNewData(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	at := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.Local)
	m.handleEvent(session.Received{Data: []byte("text\n"), At: at})
	m.switchMode()
	m.handleEvent(session.Received{Data: []byte{0x01, 'A'}, At: at})

	got := strings.Join(append(m.lines, m.currentLine), "\n")
	for _, want := range []string{"text", "00000005", "01 41", "|.A"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript %q does not contain %q", got, want)
		}
	}
}

func TestTextReceiveKeepsSGRAndFiltersLayoutControls(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.handleEvent(session.Received{Data: []byte("\x1b[31mred\x1b[0m\x1b[2J\x1bPpayload\x1b\\ok\n"), At: time.Now()})
	got := strings.Join(m.lines, "")
	if !strings.Contains(got, "\x1b[31mred\x1b[0m") {
		t.Fatalf("SGR color was not preserved: %q", got)
	}
	if strings.Contains(got, "\x1b[2J") || strings.Contains(got, "payload") || !strings.Contains(got, "ok") {
		t.Fatalf("unsafe control filtering failed: %q", got)
	}
}

func TestTextReceiveAppliesCarriageReturnRedrawWithoutDuplicatingPrompt(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.handleEvent(session.Received{Data: []byte("xsh > lxsh > ls\r\x1b[2Kxsh > "), At: time.Now()})
	if got := ansi.Strip(m.currentLine); got != "xsh > " {
		t.Fatalf("current line = %q, want %q", got, "xsh > ")
	}

	m.handleEvent(session.Received{Data: []byte("result\r\nnext"), At: time.Now()})
	if got := ansi.Strip(m.lines[len(m.lines)-1]); got != "xsh > result" {
		t.Fatalf("completed line = %q", got)
	}
}

func TestCommandPaletteInvokesRegisteredActions(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	if !m.palette {
		t.Fatal("palette did not open")
	}
	m.paletteIndex = 1
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if !m.uploadMode {
		t.Fatal("upload command did not enter path mode")
	}
}

func TestViewHasStableTerminalDimensionsWhileScrolled(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/ttyUSB0", Baud: 115200, Frame: "8,N,1"})
	m.width, m.height = 80, 24
	for i := 0; i < 100; i++ {
		m.lines = append(m.lines, strings.Repeat("界", 50))
	}
	m.scroll = 20
	m.clampScroll()
	view := m.View()
	if got := lipgloss.Height(view.Content); got != 24 {
		t.Fatalf("view height = %d, want 24", got)
	}
	for i, line := range strings.Split(view.Content, "\n") {
		if width := lipgloss.Width(line); width > 80 {
			t.Fatalf("line %d width = %d, want <= 80", i, width)
		}
	}
}

func TestCommandPaletteFloatsOverTranscriptAndUptimeIsAbsent(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.width, m.height = 80, 24
	m.lines = []string{"device output beneath popup"}
	m.palette = true
	view := m.View()
	plain := ansi.Strip(view.Content)
	for _, want := range []string{"device output beneath popup", "Command Palette"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("floating view does not contain %q: %q", want, plain)
		}
	}
	if strings.Contains(plain, "UPTIME") {
		t.Fatal("view still contains UPTIME")
	}
}

func TestSendErrorLeavesInputForCorrection(t *testing.T) {
	endpoint := newFakeEndpoint()
	endpoint.err = errors.New("write failed")
	m := newModel(endpoint, Config{})
	enterText(t, m, "retry")
	_, command := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if len(m.input) != 0 {
		t.Fatalf("input was not cleared while send is pending: %q", string(m.input))
	}
	m.Update(command())
	if string(m.input) != "retry" || !strings.Contains(m.status, "write failed") {
		t.Fatalf("input=%q status=%q", string(m.input), m.status)
	}
}

func enterText(t *testing.T, m *model, text string) {
	t.Helper()
	for _, r := range text {
		m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
}
