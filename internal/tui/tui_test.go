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
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/charmbracelet/x/ansi"
)

type fakeEndpoint struct {
	events         chan middleware.Event
	sent           [][]byte
	upload         string
	ymodemUpload   string
	ymodemDownload string
	canceled       bool
	quit           bool
	err            error
}

func newFakeEndpoint() *fakeEndpoint                    { return &fakeEndpoint{events: make(chan middleware.Event, 8)} }
func (e *fakeEndpoint) Events() <-chan middleware.Event { return e.events }
func (e *fakeEndpoint) Send(_ context.Context, data []byte) error {
	e.sent = append(e.sent, append([]byte(nil), data...))
	return e.err
}
func (e *fakeEndpoint) StartUpload(_ context.Context, path string) error {
	e.upload = path
	return e.err
}
func (e *fakeEndpoint) StartYMODEMUpload(_ context.Context, path string) error {
	e.ymodemUpload = path
	return e.err
}
func (e *fakeEndpoint) StartYMODEMDownload(_ context.Context, dir string) error {
	e.ymodemDownload = dir
	return e.err
}
func (e *fakeEndpoint) CancelTransfer() { e.canceled = true }
func (e *fakeEndpoint) Quit()           { e.quit = true }

func TestHexInputSendsExactBytesAndAddsTXTraffic(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	enterText(t, m, "AA01,ff")
	_, command := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	msg := command()
	m.Update(msg)
	if got := endpoint.sent[0]; !bytes.Equal(got, []byte{0xaa, 0x01, 0xff}) {
		t.Fatalf("hex send = %v", got)
	}
	if got := strings.Join(m.lines, "\n"); !strings.Contains(got, "TX") || !strings.Contains(got, "AA 01 FF") {
		t.Fatalf("traffic=%q", got)
	}
}

func TestConnectionEventsUpdateStatus(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.handleEvent(middleware.Disconnected{Err: errors.New("device unplugged")})
	if m.connected || !strings.Contains(m.status, "device unplugged") || !strings.Contains(m.status, "retrying") {
		t.Fatalf("disconnected status = %q", m.status)
	}
	m.handleEvent(middleware.Reconnecting{Attempt: 2, Limit: 5})
	if !strings.Contains(m.status, "2/5") {
		t.Fatalf("reconnecting status=%q", m.status)
	}

	m.handleEvent(middleware.Reconnected{})
	if !m.connected || m.status != "Serial port reconnected" {
		t.Fatalf("reconnected status = %q", m.status)
	}
}

func TestDisconnectedTUIDoesNotQueueHexInput(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	m.connected = false
	enterText(t, m, "AA 01")
	_, command := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command != nil || len(endpoint.sent) != 0 {
		t.Fatal("disconnected TUI attempted to send")
	}
	if string(m.input) != "AA 01" || !strings.Contains(m.status, "disconnected") {
		t.Fatalf("input=%q status=%q", string(m.input), m.status)
	}
}

func TestHexInputAcceptsConvenientFormsAndRejectsInvalidInput(t *testing.T) {
	for _, input := range []string{"AA 01", "AA01", "AA,01", "0xAA 0x01"} {
		data, err := parseHexInput(input)
		if err != nil || !bytes.Equal(data, []byte{0xaa, 0x01}) {
			t.Fatalf("parseHexInput(%q)=%v,%v", input, data, err)
		}
	}
	for _, input := range []string{"A", "0xAABB", "GG"} {
		t.Run(input, func(t *testing.T) {
			if _, err := parseHexInput(input); err == nil {
				t.Fatalf("parseHexInput(%q) error = nil", input)
			}
		})
	}
}

func TestTrafficTimelineShowsReceiveDirectionHexAndASCII(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	at := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.Local)
	m.handleEvent(middleware.Received{Data: []byte{0x01, 'A'}, At: at})

	got := strings.Join(m.lines, "\n")
	for _, want := range []string{"RX", "01 41", "|.A"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript %q does not contain %q", got, want)
		}
	}
}

func TestCommandPaletteInvokesRegisteredActions(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	if !m.palette {
		t.Fatal("palette did not open")
	}
	m.paletteIndex = 0
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if m.pathMode != transferRawUpload {
		t.Fatal("upload command did not enter path mode")
	}
}

func TestTUIEscCancelsRawTransfer(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	m.transferring, m.transferMode = true, transferRawUpload

	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if !endpoint.canceled || !strings.Contains(m.status, "Canceling") {
		t.Fatalf("canceled=%v status=%q", endpoint.canceled, m.status)
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
	enterText(t, m, "AA 01")
	_, command := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if len(m.input) != 0 {
		t.Fatalf("input was not cleared while send is pending: %q", string(m.input))
	}
	m.Update(command())
	if string(m.input) != "AA 01" || !strings.Contains(m.status, "write failed") {
		t.Fatalf("input=%q status=%q", string(m.input), m.status)
	}
}

func enterText(t *testing.T, m *model, text string) {
	t.Helper()
	for _, r := range text {
		m.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
}
