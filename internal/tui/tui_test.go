package tui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

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

func TestTerminalKeysSendImmediatelyAsTerminalBytes(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	keys := []tea.Key{
		{Code: 'l', Text: "ls -l"},
		{Code: tea.KeyEnter},
		{Code: 'c', Mod: tea.ModCtrl},
		{Code: tea.KeyUp},
	}
	for _, key := range keys {
		sendTerminalKey(t, m, key)
	}
	want := [][]byte{[]byte("ls -l"), {'\r'}, {0x03}, []byte("\x1b[A")}
	if len(endpoint.sent) != len(want) {
		t.Fatalf("sent %d terminal sequences, want %d", len(endpoint.sent), len(want))
	}
	for i := range want {
		if !bytes.Equal(endpoint.sent[i], want[i]) {
			t.Fatalf("sent[%d] = %q, want %q", i, endpoint.sent[i], want[i])
		}
	}
}

func TestTerminalInputPreservesKeyOrderWhileSendIsPending(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})

	_, first := m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	_, second := m.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Text: "b"}))
	if first == nil || second != nil {
		t.Fatalf("first command = %v, second command = %v", first, second)
	}
	_, second = m.Update(first())
	if second == nil {
		t.Fatal("queued second key was not sent after the first completed")
	}
	m.Update(second())

	if len(endpoint.sent) != 2 || !bytes.Equal(endpoint.sent[0], []byte("a")) || !bytes.Equal(endpoint.sent[1], []byte("b")) {
		t.Fatalf("terminal sends = %q", endpoint.sent)
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

func TestDisconnectedTUIDropsTerminalInput(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	m.connected = false
	_, command := m.Update(tea.KeyPressMsg(tea.Key{Code: 'l', Text: "ls"}))
	if command != nil || len(endpoint.sent) != 0 {
		t.Fatal("disconnected TUI attempted to send")
	}
	if !strings.Contains(m.status, "disconnected") {
		t.Fatalf("status=%q", m.status)
	}
}

func TestTerminalOutputShowsDeviceTextAndANSIColor(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.handleEvent(middleware.Received{Data: []byte("\x1b[3")})
	m.handleEvent(middleware.Received{Data: []byte("2mroot@board\x1b[0m:~# ")})
	m.handleEvent(middleware.Received{Data: []byte("uname -a\r\nLinux board\r\n")})

	got := strings.Join(m.lines, "\n")
	for _, want := range []string{"root@board:~# uname -a", "Linux board"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "\x1b") || strings.Contains(got, "RX") {
		t.Fatalf("terminal output contains ANSI controls or traffic decoration: %q", got)
	}
	styled := strings.Join(m.visualLines(80), "\n")
	if !strings.Contains(styled, "\x1b[32m") {
		t.Fatalf("terminal output lost ANSI color: %q", styled)
	}
}

func TestTerminalInterpretsClearAndCursorHome(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.resizeTerminalScreen(20, 4)
	m.appendTerminalData([]byte("old\r\ncontent"))
	m.appendTerminalData([]byte("\x1b[2J\x1b[Hroot# "))

	if got := strings.Join(m.lines[m.screenTop:m.screenTop+m.terminalRows], "\n"); got != "root# \n\n\n" {
		t.Fatalf("screen after clear = %q", got)
	}
	if m.terminalRow != 0 || m.terminalCol != len("root# ") {
		t.Fatalf("cursor after clear/home = (%d,%d)", m.terminalRow, m.terminalCol)
	}
}

func TestTerminalInterpretsCursorMovementAndCharacterEditing(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.resizeTerminalScreen(20, 3)
	m.appendTerminalData([]byte("abc\x1b[2DX\x1b[1P"))

	if got := m.lines[m.activeTerminalLine()]; got != "aX" {
		t.Fatalf("line after cursor-left and delete = %q", got)
	}
	m.appendTerminalData([]byte("\x1b[2K\rreplacement"))
	if got := m.lines[m.activeTerminalLine()]; got != "replacement" {
		t.Fatalf("line after erase = %q", got)
	}
}

func TestTerminalInterpretsBackspaceAndCursorUp(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.resizeTerminalScreen(20, 3)
	m.appendTerminalData([]byte("abc\bX\r\nsecond\x1b[A\x1b[2K\rfirst"))

	visible := m.lines[m.screenTop : m.screenTop+m.terminalRows]
	if visible[0] != "first" || visible[1] != "second" {
		t.Fatalf("screen after backspace/cursor-up = %q", visible)
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

func TestDoublePrefixSendsLiteralCtrlP(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	_, command := m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	if m.palette || command == nil {
		t.Fatalf("palette=%v command=%v", m.palette, command)
	}
	m.Update(command())
	if len(endpoint.sent) != 1 || !bytes.Equal(endpoint.sent[0], []byte{0x10}) {
		t.Fatalf("double prefix sent %q", endpoint.sent)
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

func TestViewUsesConfigurationAndTerminalColumns(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/ttyUSB0", Baud: 115200, Frame: "8,N,1"})
	m.width, m.height = 100, 28
	m.lines = []string{"root@board:~# uname -a", "Linux board"}

	plain := ansi.Strip(m.View().Content)
	for _, want := range []string{"CONFIGURATION", "/dev/ttyUSB0", "115200", "8,N,1", "TERMINAL", "root@board:~# uname -a"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("split view does not contain %q: %q", want, plain)
		}
	}
	var titles string
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "CONFIGURATION") && strings.Contains(line, "TERMINAL") {
			titles = line
			break
		}
	}
	if titles == "" || strings.Index(titles, "CONFIGURATION") >= strings.Index(titles, "TERMINAL") {
		t.Fatalf("configuration column is not left of terminal: %q", titles)
	}
	if strings.Contains(plain, "XSERIAL") || strings.Contains(plain, "Hex") || strings.Contains(plain, "input ›") {
		t.Fatalf("terminal view still contains a global title or input box: %q", plain)
	}
	lines := strings.Split(plain, "\n")
	footer := lines[len(lines)-1]
	for _, want := range []string{"CONNECTED", "Interactive", "RX", "TX", "Ready", "Ctrl+P commands"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer does not contain %q: %q", want, footer)
		}
	}
	for _, unwanted := range []string{"CONNECTION", "SESSION", "Shift+PgUp", "PgDn scroll"} {
		if strings.Contains(footer, unwanted) {
			t.Fatalf("footer still contains %q: %q", unwanted, footer)
		}
	}
	body := strings.Join(lines[:len(lines)-1], "\n")
	if strings.Contains(body, "CONNECTION") || strings.Contains(body, "SESSION") {
		t.Fatalf("connection or session remains in sidebar: %q", body)
	}
}

func TestViewShowsSteadyCursorAfterTerminalText(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/ttyUSB0", Baud: 115200, Frame: "8,N,1"})
	m.width, m.height = 100, 28
	m.appendTerminalData([]byte("root@board:~# "))

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("terminal cursor is hidden")
	}
	if view.Cursor.Blink {
		t.Fatal("terminal cursor is blinking")
	}
	wantX := sidebarWidthFor(m.width) + 1 + 2 + len("root@board:~# ")
	if view.Cursor.X != wantX || view.Cursor.Y != 3 {
		t.Fatalf("cursor = (%d,%d), want (%d,3)", view.Cursor.X, view.Cursor.Y, wantX)
	}

	m.palette = true
	if cursor := m.View().Cursor; cursor != nil {
		t.Fatalf("cursor remains visible behind command palette: %#v", cursor)
	}
}

func TestTrackpadWheelScrollsOnlyTerminalPanel(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.width, m.height = 100, 28
	for i := 0; i < 80; i++ {
		m.lines = append(m.lines, "device output")
	}

	view := m.View()
	if view.MouseMode != tea.MouseModeCellMotion || view.OnMouse == nil {
		t.Fatal("terminal mouse handling is disabled")
	}
	terminalX := sidebarWidthFor(m.width) + 2
	up := tea.MouseWheelMsg(tea.Mouse{X: terminalX, Y: 5, Button: tea.MouseWheelUp})
	command := view.OnMouse(up)
	if command == nil {
		t.Fatal("terminal wheel-up was ignored")
	}
	m.Update(command())
	if m.scroll != 3 {
		t.Fatalf("scroll after wheel-up = %d, want 3", m.scroll)
	}

	down := tea.MouseWheelMsg(tea.Mouse{X: terminalX, Y: 5, Button: tea.MouseWheelDown})
	m.Update(view.OnMouse(down)())
	if m.scroll != 0 {
		t.Fatalf("scroll after wheel-down = %d, want 0", m.scroll)
	}

	outside := tea.MouseWheelMsg(tea.Mouse{X: 2, Y: 5, Button: tea.MouseWheelUp})
	if command := view.OnMouse(outside); command != nil {
		t.Fatal("configuration-panel wheel affected terminal scroll")
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

func TestTerminalSendErrorIsReported(t *testing.T) {
	endpoint := newFakeEndpoint()
	endpoint.err = errors.New("write failed")
	m := newModel(endpoint, Config{})
	_, command := m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	m.Update(command())
	if !strings.Contains(m.status, "write failed") {
		t.Fatalf("status=%q", m.status)
	}
}

func sendTerminalKey(t *testing.T, m *model, key tea.Key) {
	t.Helper()
	_, command := m.Update(tea.KeyPressMsg(key))
	if command == nil {
		t.Fatalf("key %q did not enqueue terminal bytes", key.Keystroke())
	}
	_, next := m.Update(command())
	if next != nil {
		t.Fatalf("key %q left an unexpected queued send", key.Keystroke())
	}
}
