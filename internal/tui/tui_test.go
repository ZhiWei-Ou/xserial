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
	configured     middleware.ConnectionConfig
	configureErr   error
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
func (e *fakeEndpoint) Configure(_ context.Context, cfg middleware.ConnectionConfig) error {
	e.configured = cfg
	return e.configureErr
}

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

func TestConnectionEventsOnlyUpdateIndicator(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.handleEvent(middleware.Disconnected{Err: errors.New("device unplugged")})
	if m.connected || m.status != "Ready" {
		t.Fatalf("connected=%v status=%q after disconnect", m.connected, m.status)
	}
	m.handleEvent(middleware.Reconnecting{Attempt: 2})
	if m.connected || m.status != "Ready" {
		t.Fatalf("connected=%v status=%q while reconnecting", m.connected, m.status)
	}

	m.handleEvent(middleware.Reconnected{})
	if !m.connected || m.status != "Ready" {
		t.Fatalf("connected=%v status=%q after reconnect", m.connected, m.status)
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
	if m.status != "Ready" {
		t.Fatalf("status=%q, want Ready", m.status)
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

func TestCommandPaletteSupportsVimNavigation(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{})
	m.palette = true
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	if m.paletteIndex != 1 {
		t.Fatalf("palette index after j = %d, want 1", m.paletteIndex)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}))
	if m.paletteIndex != 0 {
		t.Fatalf("palette index after k = %d, want 0", m.paletteIndex)
	}
}

func TestPrefixCFocusesConfigurationWithoutSendingSerialBytes(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.width, m.height = 100, 28

	m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Mod: tea.ModCtrl}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"}))
	if m.palette || m.focus != focusConfiguration || m.configurationFocusIndex != 0 {
		t.Fatalf("palette=%v focus=%v index=%d", m.palette, m.focus, m.configurationFocusIndex)
	}
	if len(endpoint.sent) != 0 {
		t.Fatalf("Ctrl+P c sent serial bytes %q", endpoint.sent)
	}
}

func TestConfigurationFocusNavigatesAndReturnsFromEditor(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.focus = focusConfiguration

	m.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}))
	if m.configurationFocusIndex != 4 {
		t.Fatalf("index after wrapped k = %d, want 4", m.configurationFocusIndex)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if m.configurationFocusIndex != 3 {
		t.Fatalf("selected index = %d, want parity index 3", m.configurationFocusIndex)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if m.configuration.mode != configurationFrame || m.configuration.index != 1 || !m.configuration.returnToConfiguration {
		t.Fatalf("editor mode=%v index=%d return=%v", m.configuration.mode, m.configuration.index, m.configuration.returnToConfiguration)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	if m.configuration.mode != configurationNone || m.focus != focusConfiguration || m.configurationFocusIndex != 3 {
		t.Fatalf("mode=%v focus=%v index=%d after editor close", m.configuration.mode, m.focus, m.configurationFocusIndex)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if m.focus != focusTerminal {
		t.Fatalf("focus after Esc = %v, want terminal", m.focus)
	}
}

func TestConfigurationApplyReturnsToConfigurationFocus(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.focus, m.configurationFocusIndex = focusConfiguration, 1
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m.configuration.input = []rune("921600")
	_, apply := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if apply == nil {
		t.Fatal("baud editor did not apply")
	}
	m.Update(apply())
	if m.focus != focusConfiguration || m.configuration.mode != configurationNone || m.baud != 921600 {
		t.Fatalf("focus=%v mode=%v baud=%d", m.focus, m.configuration.mode, m.baud)
	}
}

func TestQClosesSelectionPopupsButRemainsValidPathInput(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.palette = true
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	if m.palette {
		t.Fatal("q did not close command palette")
	}

	m.openConfiguration(configurationFrame)
	m.status = "temporary popup status"
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	if m.configuration.mode != configurationNone {
		t.Fatal("q did not close configuration popup")
	}
	if m.status != "Ready" {
		t.Fatalf("status after closing configuration = %q, want Ready", m.status)
	}

	m.pathMode = transferRawUpload
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	if m.pathMode != transferRawUpload || string(m.input) != "q" {
		t.Fatalf("path mode = %v, input = %q", m.pathMode, string(m.input))
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
	for _, want := range []string{"CONFIGURATION", "/dev/ttyUSB0", "115200", "DATA BITS", "PARITY", "STOP BITS", "NONE", "TERMINAL", "root@board:~# uname -a"} {
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
	if strings.Contains(body, "FRAME") {
		t.Fatalf("combined frame field remains in sidebar: %q", body)
	}
	if strings.Contains(body, "›") {
		t.Fatalf("sidebar still contains click arrows: %q", body)
	}
}

func TestExpandedFrameFieldsOpenTheirMatchingEditorRow(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	handler := mainMouseHandler(true, 25, 26, 74, 27)
	for _, test := range []struct {
		y, want int
	}{
		{y: 7, want: 0},
		{y: 9, want: 1},
		{y: 11, want: 2},
	} {
		click := tea.MouseClickMsg(tea.Mouse{X: 2, Y: test.y, Button: tea.MouseLeft})
		command := handler(click)
		if command == nil {
			t.Fatalf("click at y=%d was ignored", test.y)
		}
		m.Update(command())
		if m.configuration.mode != configurationFrame || m.configuration.index != test.want {
			t.Fatalf("click at y=%d opened mode=%v index=%d, want frame index=%d", test.y, m.configuration.mode, m.configuration.index, test.want)
		}
		m.configuration = configurationState{}
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

func TestConfigurationFocusHighlightsFieldAndTerminalClickRestoresCursor(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.width, m.height = 100, 28
	m.appendTerminalData([]byte("root# "))
	m.focus, m.configurationFocusIndex = focusConfiguration, 3

	view := m.View()
	if view.Cursor != nil {
		t.Fatalf("terminal cursor remains visible during configuration focus: %#v", view.Cursor)
	}
	plain := ansi.Strip(view.Content)
	focusedLabel := m.renderSidebarField(3, "PARITY", "NONE")[0]
	m.focus = focusTerminal
	normalLabel := m.renderSidebarField(3, "PARITY", "NONE")[0]
	m.focus = focusConfiguration
	if !strings.Contains(plain, "j/k select") || focusedLabel == normalLabel {
		t.Fatalf("configuration focus is not visible: %q", plain)
	}
	terminalX := sidebarWidthFor(m.width) + 4
	click := tea.MouseClickMsg(tea.Mouse{X: terminalX, Y: 5, Button: tea.MouseLeft})
	command := view.OnMouse(click)
	if command == nil {
		t.Fatal("terminal click was ignored")
	}
	m.Update(command())
	if m.focus != focusTerminal || m.View().Cursor == nil {
		t.Fatalf("focus=%v cursor=%#v after terminal click", m.focus, m.View().Cursor)
	}
}

func TestTerminalKeysRemainTransparentAfterLeavingConfigurationFocus(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{})
	m.focus = focusConfiguration
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	sendTerminalKey(t, m, tea.Key{Code: 'c', Text: "c"})
	if len(endpoint.sent) != 1 || !bytes.Equal(endpoint.sent[0], []byte("c")) {
		t.Fatalf("terminal sends = %q", endpoint.sent)
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

func TestConfigurationSidebarClickSelectsLivePortAndApplies(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{
		PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1",
		ListPorts: func() ([]PortOption, error) {
			return []PortOption{{Name: "/dev/test1"}, {Name: "/dev/test0"}}, nil
		},
	})
	m.width, m.height = 100, 28

	view := m.View()
	click := tea.MouseClickMsg(tea.Mouse{X: 2, Y: 4, Button: tea.MouseLeft})
	open := view.OnMouse(click)
	if open == nil {
		t.Fatal("port field click was ignored")
	}
	_, load := m.Update(open())
	if m.configuration.mode != configurationPort || load == nil {
		t.Fatalf("configuration mode = %v, load = %v", m.configuration.mode, load)
	}
	m.Update(load())
	if len(m.configuration.ports) != 2 || m.configuration.ports[m.configuration.index].Name != "/dev/test0" {
		t.Fatalf("ports = %#v, index = %d", m.configuration.ports, m.configuration.index)
	}

	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	_, apply := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if apply == nil {
		t.Fatal("selected port was not applied")
	}
	m.Update(apply())
	if endpoint.configured.PortName != "/dev/test1" || m.portName != "/dev/test1" {
		t.Fatalf("endpoint config = %#v, model port = %q", endpoint.configured, m.portName)
	}
}

func TestPortRefreshPreservesSelectionAndAddsNewPorts(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{
		PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1",
		ListPorts: func() ([]PortOption, error) { return nil, nil },
	})
	m.openConfiguration(configurationPort)
	m.handlePortsLoaded(portsLoadedMsg{ports: []PortOption{{Name: "/dev/test0"}, {Name: "/dev/test1"}}})
	m.configuration.index = 1
	m.handlePortsLoaded(portsLoadedMsg{ports: []PortOption{{Name: "/dev/test2"}, {Name: "/dev/test1"}}})

	if len(m.configuration.ports) != 2 || m.configuration.ports[m.configuration.index].Name != "/dev/test1" {
		t.Fatalf("refreshed ports = %#v, index = %d", m.configuration.ports, m.configuration.index)
	}
}

func TestPortConfigurationSupportsVimNavigation(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.configuration = configurationState{
		mode:  configurationPort,
		ports: []PortOption{{Name: "/dev/test0"}, {Name: "/dev/test1"}},
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	if m.configuration.index != 1 {
		t.Fatalf("port index after j = %d, want 1", m.configuration.index)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}))
	if m.configuration.index != 0 {
		t.Fatalf("port index after k = %d, want 0", m.configuration.index)
	}
}

func TestPortPopupKeepsNameOnOneLineAndMovesDetailsBelowList(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/cu.usbmodem1102", Baud: 115200, Frame: "8,N,1"})
	m.configuration = configurationState{
		mode: configurationPort,
		ports: []PortOption{{
			Name:   "/dev/cu.usbmodem1102",
			Detail: "vid=0483 pid=3754 serial=0045002D353 product=STM32 Virtual COM Port",
		}},
	}

	plain := ansi.Strip(m.renderPortPopup(48))
	var portLine string
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "/dev/cu.usbmodem1102") {
			portLine = line
			break
		}
	}
	if portLine == "" || strings.Contains(portLine, "vid=") || !strings.Contains(plain, "DETAILS") || !strings.Contains(plain, "vid=0483") {
		t.Fatalf("port popup = %q", plain)
	}
	details := portDetailLines(strings.Repeat("device-info ", 20), 24)
	if len(details) != 2 || lipgloss.Width(details[0]) > 24 || lipgloss.Width(details[1]) > 24 {
		t.Fatalf("detail lines = %#v", details)
	}
}

func TestBaudConfigurationAcceptsArbitraryPositiveRate(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.openConfiguration(configurationBaud)
	for range len("115200") {
		m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: '9', Text: "921600"}))
	_, apply := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m.Update(apply())
	if endpoint.configured.BaudRate != 921600 || m.baud != 921600 {
		t.Fatalf("endpoint config = %#v, model baud = %d", endpoint.configured, m.baud)
	}
}

func TestFrameConfigurationEditsEachFieldBeforeApply(t *testing.T) {
	endpoint := newFakeEndpoint()
	m := newModel(endpoint, Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.openConfiguration(configurationFrame)

	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyRight}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	_, apply := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m.Update(apply())

	got := endpoint.configured
	if got.DataBits != 7 || got.Parity != "odd" || got.StopBits != "1.5" || m.frameName() != "7,O,1.5" {
		t.Fatalf("frame config = %#v, display = %q", got, m.frameName())
	}
}

func TestFrameConfigurationSupportsVimNavigation(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.openConfiguration(configurationFrame)
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'h', Text: "h"}))
	if m.configuration.draft.DataBits != 7 {
		t.Fatalf("data bits after h = %d, want 7", m.configuration.draft.DataBits)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'l', Text: "l"}))
	if m.configuration.index != 1 || m.configuration.draft.Parity != "odd" {
		t.Fatalf("frame index = %d, parity after j/l = %q", m.configuration.index, m.configuration.draft.Parity)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"}))
	if m.configuration.index != 0 {
		t.Fatalf("frame index after k = %d, want 0", m.configuration.index)
	}
}

func TestBaudInputKeepsVimKeysAsTextInput(t *testing.T) {
	m := newModel(newFakeEndpoint(), Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.openConfiguration(configurationBaud)
	m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	if got := string(m.configuration.input); got != "115200" {
		t.Fatalf("baud input after j = %q, want unchanged numeric input", got)
	}
}

func TestConfigurationFailureKeepsPreviousValuesAndPopupOpen(t *testing.T) {
	endpoint := newFakeEndpoint()
	endpoint.configureErr = errors.New("port busy")
	m := newModel(endpoint, Config{PortName: "/dev/test0", Baud: 115200, Frame: "8,N,1"})
	m.openConfiguration(configurationBaud)
	m.configuration.input = []rune("921600")
	_, apply := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m.Update(apply())

	if m.baud != 115200 || m.configuration.mode != configurationBaud || !strings.Contains(m.status, "port busy") {
		t.Fatalf("baud = %d, mode = %v, status = %q", m.baud, m.configuration.mode, m.status)
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
