package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/charmbracelet/x/ansi"
)

const maxTranscriptLines = 5000

type Config struct {
	Input      io.Reader
	Output     io.Writer
	TimeFormat string
	PortName   string
	Baud       int
	Frame      string
	DataBits   int
	Parity     string
	StopBits   string
	ListPorts  func() ([]PortOption, error)
}

type PortOption struct {
	Name   string
	Detail string
}

type Frontend struct{ cfg Config }

func New(cfg Config) *Frontend { return &Frontend{cfg: cfg} }

func (f *Frontend) Run(ctx context.Context, endpoint middleware.Endpoint) error {
	if f.cfg.Input == nil {
		return errors.New("TUI input is nil")
	}
	if f.cfg.Output == nil {
		return errors.New("TUI output is nil")
	}
	m := newModel(endpoint, f.cfg)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(f.cfg.Input), tea.WithOutput(f.cfg.Output))
	final, err := program.Run()
	if model, ok := final.(*model); ok && model.err != nil {
		return model.err
	}
	if err != nil && ctx.Err() == nil && !errors.Is(err, tea.ErrInterrupted) && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	return nil
}

type transferMode int

const (
	transferNone transferMode = iota
	transferRawUpload
)

type endpointEventMsg struct{ event middleware.Event }
type endpointClosedMsg struct{}
type terminalScrollMsg int
type terminalFocusMsg struct{}
type configurationOpenMsg struct {
	mode  configurationMode
	index int
}
type configurationClickMsg struct {
	mode  configurationMode
	index int
}
type sendResultMsg struct {
	data []byte
	err  error
}

type command struct {
	key     string
	label   string
	enabled func(*model) bool
	run     func(*model) tea.Cmd
}

var commands = []command{
	{label: "Send raw file", enabled: func(m *model) bool { return m.connected && !m.transferring }, run: func(m *model) tea.Cmd {
		m.pathMode, m.input, m.status = transferRawUpload, nil, "Enter a local file path for raw upload"
		return nil
	}},
	{label: "Clear terminal", run: func(m *model) tea.Cmd { m.clearTranscript(); return nil }},
	{key: "c", label: "Focus configuration", enabled: func(m *model) bool { return !m.transferring && showSidebar(max(20, m.width)) }, run: func(m *model) tea.Cmd {
		m.focus, m.configurationFocusIndex = focusConfiguration, 0
		return nil
	}},
	{label: "Cancel transfer", enabled: func(m *model) bool { return m.transferring }, run: func(m *model) tea.Cmd {
		m.endpoint.CancelTransfer()
		m.status = "Canceling transfer…"
		return nil
	}},
	{label: "Quit", run: func(m *model) tea.Cmd { m.endpoint.Quit(); return tea.Quit }},
}

type model struct {
	endpoint                middleware.Endpoint
	listPorts               func() ([]PortOption, error)
	portName                string
	baud                    int
	dataBits                int
	parity                  string
	stopBits                string
	width                   int
	height                  int
	lines                   []string
	input                   []rune
	pathMode                transferMode
	transferMode            transferMode
	transferring            bool
	connected               bool
	palette                 bool
	paletteIndex            int
	status                  string
	scroll                  int
	rxBytes                 int64
	txBytes                 int64
	err                     error
	sendQueue               [][]byte
	sending                 bool
	terminalCol             int
	terminalRow             int
	terminalCols            int
	terminalRows            int
	screenTop               int
	savedCol                int
	savedRow                int
	terminalSGR             string
	terminalANSI            []byte
	lineStyles              [][]string
	configuration           configurationState
	focus                   focusTarget
	configurationFocusIndex int
}

func newModel(endpoint middleware.Endpoint, cfg Config) *model {
	dataBits, parity, stopBits := connectionFrame(cfg)
	return &model{
		endpoint: endpoint, listPorts: cfg.ListPorts, portName: cfg.PortName,
		baud: cfg.Baud, dataBits: dataBits, parity: parity, stopBits: stopBits,
		connected: true, status: "Ready",
		lines: []string{""}, lineStyles: [][]string{nil}, terminalCols: 80, terminalRows: 1,
	}
}

func (m *model) Init() tea.Cmd { return waitEvent(m.endpoint.Events()) }

func waitEvent(events <-chan middleware.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return endpointClosedMsg{}
		}
		return endpointEventMsg{event: event}
	}
}

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
	case endpointClosedMsg:
		return m, tea.Quit
	case endpointEventMsg:
		m.handleEvent(msg.event)
		return m, waitEvent(m.endpoint.Events())
	case sendResultMsg:
		m.sending = false
		if msg.err != nil && !errors.Is(msg.err, middleware.ErrDisconnected) {
			m.status = fmt.Sprintf("Send failed: %v", msg.err)
		} else {
			if msg.err == nil {
				m.txBytes += int64(len(msg.data))
			}
		}
		return m, m.sendNext()
	case terminalScrollMsg:
		m.scroll += int(msg)
		m.clampScroll()
	case terminalFocusMsg:
		m.focus = focusTerminal
	case configurationOpenMsg:
		returnToConfiguration := m.focus == focusConfiguration
		command := m.openConfiguration(msg.mode)
		if msg.mode == configurationFrame {
			m.configuration.index = msg.index
		}
		if m.configuration.mode != configurationNone {
			m.configuration.returnToConfiguration = returnToConfiguration
		}
		return m, command
	case configurationClickMsg:
		return m, m.handleConfigurationClick(msg)
	case portsLoadedMsg:
		return m, m.handlePortsLoaded(msg)
	case refreshPortsMsg:
		if m.configuration.mode == configurationPort {
			return m, m.loadPorts()
		}
	case configurationAppliedMsg:
		m.handleConfigurationApplied(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleEvent(event middleware.Event) {
	switch event := event.(type) {
	case middleware.Disconnected:
		m.connected = false
		m.sendQueue = nil
	case middleware.Reconnecting:
		m.connected = false
	case middleware.Reconnected:
		m.connected = true
	case middleware.Received:
		atBottom := m.scroll == 0
		before := len(m.visualLines(m.transcriptWidth()))
		m.appendTerminalData(event.Data)
		m.rxBytes += int64(len(event.Data))
		m.trimTranscript()
		if !atBottom {
			after := len(m.visualLines(m.transcriptWidth()))
			m.scroll += after - before
		}
		m.clampScroll()
	case middleware.UploadStarted:
		m.transferMode, m.transferring = transferRawUpload, true
		m.status = fmt.Sprintf("Uploading %s…", event.Path)
	case middleware.UploadProgress:
		m.status = fmt.Sprintf("Uploading %s — %s / %s", event.Path, formatBytes(event.Written), formatBytes(event.Total))
	case middleware.UploadFinished:
		m.transferMode, m.transferring = transferNone, false
		if event.Err != nil {
			m.status = fmt.Sprintf("Upload failed: %v", event.Err)
		} else {
			m.txBytes += event.Bytes
			m.status = fmt.Sprintf("Uploaded %s (%s)", event.Path, formatBytes(event.Bytes))
		}
	}
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Keystroke()
	if m.configuration.mode != configurationNone {
		return m.handleConfigurationKey(msg)
	}
	if m.focus == focusConfiguration {
		return m.handleConfigurationFocusKey(key)
	}
	if key == "ctrl+p" {
		if m.palette {
			m.palette = false
			if m.connected && !m.transferring && m.pathMode == transferNone {
				return m, m.queueSend([]byte{0x10})
			}
			return m, nil
		}
		m.palette = !m.palette
		m.paletteIndex = 0
		return m, nil
	}
	if m.palette {
		return m.handlePalette(key)
	}
	if key == "esc" && m.transferring {
		m.endpoint.CancelTransfer()
		m.status = "Canceling transfer…"
		return m, nil
	}
	if m.pathMode != transferNone {
		return m.handlePathInput(msg)
	}
	if m.transferring {
		return m, nil
	}
	switch key {
	case "shift+pgup":
		m.scroll += max(1, m.transcriptHeight()-1)
		m.clampScroll()
		return m, nil
	case "shift+pgdown":
		m.scroll -= max(1, m.transcriptHeight()-1)
		if m.scroll < 0 {
			m.scroll = 0
		}
		return m, nil
	}
	if !m.connected {
		return m, nil
	}
	return m, m.queueSend(terminalKeyBytes(msg.Key()))
}

func (m *model) handlePathInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "esc":
		m.pathMode = transferNone
		m.input = nil
		m.status = "File selection canceled"
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case "enter":
		return m.startFileTransfer()
	default:
		if msg.Key().Text != "" {
			m.input = append(m.input, []rune(msg.Key().Text)...)
		}
	}
	return m, nil
}

func (m *model) handlePalette(key string) (tea.Model, tea.Cmd) {
	for index, cmd := range commands {
		if cmd.key == key {
			m.paletteIndex = index
			return m.runPaletteCommand(cmd)
		}
	}
	key = navigationKey(key)
	switch key {
	case "esc", "q":
		m.palette = false
	case "up":
		m.paletteIndex = (m.paletteIndex - 1 + len(commands)) % len(commands)
	case "down":
		m.paletteIndex = (m.paletteIndex + 1) % len(commands)
	case "enter":
		return m.runPaletteCommand(commands[m.paletteIndex])
	}
	return m, nil
}

func (m *model) runPaletteCommand(cmd command) (tea.Model, tea.Cmd) {
	if cmd.enabled != nil && !cmd.enabled(m) {
		m.status = cmd.label + " is unavailable"
		return m, nil
	}
	m.palette = false
	return m, cmd.run(m)
}

func navigationKey(key string) string {
	switch key {
	case "h":
		return "left"
	case "j":
		return "down"
	case "k":
		return "up"
	case "l":
		return "right"
	default:
		return key
	}
}

func (m *model) clearTranscript() {
	m.resetTerminalScreen()
	m.terminalANSI = nil
	m.scroll = 0
	m.status = "Terminal cleared"
}

func (m *model) queueSend(data []byte) tea.Cmd {
	if len(data) == 0 {
		return nil
	}
	m.sendQueue = append(m.sendQueue, append([]byte(nil), data...))
	return m.sendNext()
}

func (m *model) sendNext() tea.Cmd {
	if m.sending || len(m.sendQueue) == 0 {
		return nil
	}
	data := m.sendQueue[0]
	m.sendQueue = m.sendQueue[1:]
	m.sending = true
	return func() tea.Msg {
		err := m.endpoint.Send(context.Background(), data)
		return sendResultMsg{data: data, err: err}
	}
}

func terminalKeyBytes(key tea.Key) []byte {
	if key.Mod&tea.ModCtrl != 0 {
		code := unicode.ToLower(key.Code)
		if code >= 'a' && code <= 'z' {
			return withAlt(key.Mod, []byte{byte(code-'a') + 1})
		}
		switch code {
		case ' ', '@':
			return withAlt(key.Mod, []byte{0})
		case '[':
			return withAlt(key.Mod, []byte{0x1b})
		case '\\':
			return withAlt(key.Mod, []byte{0x1c})
		case ']':
			return withAlt(key.Mod, []byte{0x1d})
		case '^':
			return withAlt(key.Mod, []byte{0x1e})
		case '_':
			return withAlt(key.Mod, []byte{0x1f})
		}
	}

	if key.Text != "" {
		return withAlt(key.Mod, []byte(key.Text))
	}

	var data []byte
	switch key.Code {
	case tea.KeyEnter, tea.KeyKpEnter:
		data = []byte{'\r'}
	case tea.KeyTab:
		if key.Mod&tea.ModShift != 0 {
			data = []byte("\x1b[Z")
		} else {
			data = []byte{'\t'}
		}
	case tea.KeyBackspace:
		data = []byte{0x7f}
	case tea.KeyEscape:
		data = []byte{0x1b}
	case tea.KeyUp:
		data = []byte("\x1b[A")
	case tea.KeyDown:
		data = []byte("\x1b[B")
	case tea.KeyRight:
		data = []byte("\x1b[C")
	case tea.KeyLeft:
		data = []byte("\x1b[D")
	case tea.KeyHome:
		data = []byte("\x1b[H")
	case tea.KeyEnd:
		data = []byte("\x1b[F")
	case tea.KeyInsert:
		data = []byte("\x1b[2~")
	case tea.KeyDelete:
		data = []byte("\x1b[3~")
	case tea.KeyPgUp:
		data = []byte("\x1b[5~")
	case tea.KeyPgDown:
		data = []byte("\x1b[6~")
	}
	return withAlt(key.Mod, data)
}

func withAlt(mod tea.KeyMod, data []byte) []byte {
	if len(data) == 0 || mod&tea.ModAlt == 0 {
		return data
	}
	return append([]byte{0x1b}, data...)
}

func (m *model) startFileTransfer() (tea.Model, tea.Cmd) {
	path := strings.TrimSpace(string(m.input))
	if path == "" {
		m.pathMode = transferNone
		m.status = "File selection canceled"
		return m, nil
	}
	mode := m.pathMode
	m.pathMode = transferNone
	m.input = nil
	err := m.endpoint.StartUpload(context.Background(), path)
	if err != nil {
		m.status = fmt.Sprintf("Transfer failed: %v", err)
		return m, nil
	}
	m.transferMode, m.transferring = mode, true
	m.status = "Starting " + mode.label() + "…"
	return m, nil
}

func (m transferMode) label() string {
	switch m {
	case transferRawUpload:
		return "raw upload"
	default:
		return "transfer"
	}
}

func (m *model) appendTerminalData(data []byte) {
	m.terminalANSI = append(m.terminalANSI, data...)
	m.ensureTerminalScreen()
	for len(m.terminalANSI) > 0 {
		if m.terminalANSI[0] == 0x1b {
			consumed, complete := m.consumeANSI(m.terminalANSI)
			if !complete {
				break
			}
			m.terminalANSI = m.terminalANSI[consumed:]
			continue
		}

		var r rune
		if m.terminalANSI[0] < utf8.RuneSelf {
			r = rune(m.terminalANSI[0])
			m.terminalANSI = m.terminalANSI[1:]
		} else {
			if !utf8.FullRune(m.terminalANSI) {
				break
			}
			var size int
			r, size = utf8.DecodeRune(m.terminalANSI)
			m.terminalANSI = m.terminalANSI[size:]
		}
		switch r {
		case '\r':
			m.terminalCol = 0
		case '\n':
			m.terminalLineFeed()
		case '\b':
			if m.terminalCol > 0 {
				m.terminalCol--
			}
		case '\t':
			spaces := 8 - m.terminalCol%8
			for range spaces {
				m.putTerminalRune(' ')
			}
		default:
			if unicode.IsPrint(r) {
				m.putTerminalRune(r)
			}
		}
	}
	m.trimTranscript()
}

func (m *model) consumeANSI(data []byte) (consumed int, complete bool) {
	if len(data) < 2 {
		return 0, false
	}
	switch data[1] {
	case '[':
		for i := 2; i < len(data); i++ {
			if data[i] < 0x40 || data[i] > 0x7e {
				continue
			}
			params := data[2:i]
			if data[i] == 'm' {
				sequence := string(data[:i+1])
				if resetsSGR(params) {
					m.terminalSGR = sequence
				} else {
					m.terminalSGR += sequence
				}
			} else {
				m.applyCSI(data[i], parseCSIParams(params))
			}
			return i + 1, true
		}
		return 0, false
	case ']':
		for i := 2; i < len(data); i++ {
			if data[i] == '\a' {
				return i + 1, true
			}
			if data[i] == 0x1b {
				if i+1 >= len(data) {
					return 0, false
				}
				if data[i+1] == '\\' {
					return i + 2, true
				}
			}
		}
		return 0, false
	case '7':
		m.savedRow, m.savedCol = m.terminalRow, m.terminalCol
		return 2, true
	case '8':
		m.terminalRow, m.terminalCol = m.savedRow, m.savedCol
		m.clampTerminalCursor()
		return 2, true
	case 'D':
		m.terminalLineFeed()
		return 2, true
	case 'E':
		m.terminalCol = 0
		m.terminalLineFeed()
		return 2, true
	case 'M':
		m.terminalReverseIndex()
		return 2, true
	case 'c':
		m.resetTerminalScreen()
		return 2, true
	case '(', ')', '*', '+':
		if len(data) < 3 {
			return 0, false
		}
		return 3, true
	default:
		return 2, true
	}
}

func parseCSIParams(raw []byte) []int {
	text := strings.TrimLeft(string(raw), "?<=>!")
	if text == "" {
		return nil
	}
	fields := strings.Split(text, ";")
	params := make([]int, len(fields))
	for i, field := range fields {
		field = strings.SplitN(field, ":", 2)[0]
		if field == "" {
			continue
		}
		params[i], _ = strconv.Atoi(field)
	}
	return params
}

func csiParam(params []int, index, defaultValue int) int {
	if index >= len(params) || params[index] == 0 {
		return defaultValue
	}
	return params[index]
}

func (m *model) applyCSI(final byte, params []int) {
	switch final {
	case 'A':
		m.terminalRow -= csiParam(params, 0, 1)
	case 'B':
		m.terminalRow += csiParam(params, 0, 1)
	case 'C':
		m.terminalCol += csiParam(params, 0, 1)
	case 'D':
		m.terminalCol -= csiParam(params, 0, 1)
	case 'E':
		m.terminalRow += csiParam(params, 0, 1)
		m.terminalCol = 0
	case 'F':
		m.terminalRow -= csiParam(params, 0, 1)
		m.terminalCol = 0
	case 'G':
		m.terminalCol = csiParam(params, 0, 1) - 1
	case 'H', 'f':
		m.terminalRow = csiParam(params, 0, 1) - 1
		m.terminalCol = csiParam(params, 1, 1) - 1
	case 'd':
		m.terminalRow = csiParam(params, 0, 1) - 1
	case 'J':
		m.eraseTerminalDisplay(csiParam(params, 0, 0))
	case 'K':
		m.eraseTerminalLine(csiParam(params, 0, 0))
	case 'P':
		m.deleteTerminalChars(csiParam(params, 0, 1))
	case '@':
		m.insertTerminalChars(csiParam(params, 0, 1))
	case 'X':
		m.eraseTerminalChars(csiParam(params, 0, 1))
	case 'L':
		m.insertTerminalLines(csiParam(params, 0, 1))
	case 'M':
		m.deleteTerminalLines(csiParam(params, 0, 1))
	case 'S':
		m.scrollTerminalUp(csiParam(params, 0, 1))
	case 'T':
		m.scrollTerminalDown(csiParam(params, 0, 1))
	case 's':
		m.savedRow, m.savedCol = m.terminalRow, m.terminalCol
	case 'u':
		m.terminalRow, m.terminalCol = m.savedRow, m.savedCol
	}
	m.clampTerminalCursor()
}

func resetsSGR(params []byte) bool {
	if len(params) == 0 {
		return true
	}
	for _, param := range strings.Split(string(params), ";") {
		if param == "" || param == "0" {
			return true
		}
	}
	return false
}

func (m *model) putTerminalRune(r rune) {
	if m.terminalCol >= m.terminalCols {
		m.terminalCol = 0
		m.terminalLineFeed()
	}
	index := m.activeTerminalLine()
	line := []rune(m.lines[index])
	styles := m.lineStyles[index]
	for len(styles) < len(line) {
		styles = append(styles, "")
	}
	for len(line) < m.terminalCol {
		line = append(line, ' ')
		styles = append(styles, m.terminalSGR)
	}
	if m.terminalCol < len(line) {
		line[m.terminalCol] = r
		styles[m.terminalCol] = m.terminalSGR
	} else {
		line = append(line, r)
		styles = append(styles, m.terminalSGR)
	}
	m.lines[index] = string(line)
	m.lineStyles[index] = styles
	m.terminalCol++
}

func (m *model) activeTerminalLine() int {
	m.ensureTerminalScreen()
	return m.screenTop + m.terminalRow
}

func (m *model) ensureTerminalScreen() {
	if m.terminalCols < 1 {
		m.terminalCols = 1
	}
	if m.terminalRows < 1 {
		m.terminalRows = 1
	}
	needed := m.screenTop + m.terminalRows
	for len(m.lines) < needed {
		m.lines = append(m.lines, "")
	}
	for len(m.lineStyles) < len(m.lines) {
		m.lineStyles = append(m.lineStyles, nil)
	}
}

func (m *model) resizeTerminalScreen(cols, rows int) {
	cols, rows = max(1, cols), max(1, rows)
	absoluteCursor := m.screenTop + m.terminalRow
	m.terminalCols, m.terminalRows = cols, rows
	m.screenTop = max(0, len(m.lines)-rows)
	m.terminalRow = absoluteCursor - m.screenTop
	m.clampTerminalCursor()
	m.ensureTerminalScreen()
}

func (m *model) clampTerminalCursor() {
	m.terminalRow = min(max(0, m.terminalRow), max(0, m.terminalRows-1))
	m.terminalCol = min(max(0, m.terminalCol), max(0, m.terminalCols-1))
}

func (m *model) terminalLineFeed() {
	if m.terminalRow < m.terminalRows-1 {
		m.terminalRow++
		m.ensureTerminalScreen()
		return
	}
	m.screenTop++
	m.ensureTerminalScreen()
}

func (m *model) terminalReverseIndex() {
	if m.terminalRow > 0 {
		m.terminalRow--
		return
	}
	m.ensureTerminalScreen()
	index := m.screenTop
	m.lines = append(m.lines[:index], append([]string{""}, m.lines[index:]...)...)
	m.lineStyles = append(m.lineStyles[:index], append([][]string{nil}, m.lineStyles[index:]...)...)
	bottom := m.screenTop + m.terminalRows
	m.lines = append(m.lines[:bottom], m.lines[bottom+1:]...)
	m.lineStyles = append(m.lineStyles[:bottom], m.lineStyles[bottom+1:]...)
}

func (m *model) resetTerminalScreen() {
	m.lines = make([]string, max(1, m.terminalRows))
	m.lineStyles = make([][]string, len(m.lines))
	m.screenTop, m.terminalRow, m.terminalCol = 0, 0, 0
	m.savedRow, m.savedCol = 0, 0
	m.terminalSGR = ""
}

func (m *model) eraseTerminalDisplay(mode int) {
	m.ensureTerminalScreen()
	switch mode {
	case 0:
		m.eraseTerminalLine(0)
		for row := m.terminalRow + 1; row < m.terminalRows; row++ {
			m.clearTerminalRow(row)
		}
	case 1:
		for row := 0; row < m.terminalRow; row++ {
			m.clearTerminalRow(row)
		}
		m.eraseTerminalLine(1)
	case 2:
		for row := range m.terminalRows {
			m.clearTerminalRow(row)
		}
	case 3:
		m.lines = append([]string(nil), m.lines[m.screenTop:]...)
		m.lineStyles = append([][]string(nil), m.lineStyles[m.screenTop:]...)
		m.screenTop = 0
	}
}

func (m *model) eraseTerminalLine(mode int) {
	index := m.activeTerminalLine()
	line := []rune(m.lines[index])
	styles := m.lineStyles[index]
	for len(styles) < len(line) {
		styles = append(styles, "")
	}
	switch mode {
	case 0:
		if m.terminalCol < len(line) {
			line = line[:m.terminalCol]
			styles = styles[:min(m.terminalCol, len(styles))]
		}
	case 1:
		end := min(m.terminalCol+1, len(line))
		for i := 0; i < end; i++ {
			line[i] = ' '
			if i < len(styles) {
				styles[i] = ""
			}
		}
	case 2:
		line, styles = nil, nil
	}
	m.lines[index], m.lineStyles[index] = string(line), styles
}

func (m *model) deleteTerminalChars(count int) {
	index := m.activeTerminalLine()
	line := []rune(m.lines[index])
	if m.terminalCol >= len(line) {
		return
	}
	end := min(len(line), m.terminalCol+count)
	line = append(line[:m.terminalCol], line[end:]...)
	styles := m.lineStyles[index]
	if m.terminalCol < len(styles) {
		styleEnd := min(len(styles), end)
		styles = append(styles[:m.terminalCol], styles[styleEnd:]...)
	}
	m.lines[index], m.lineStyles[index] = string(line), styles
}

func (m *model) insertTerminalChars(count int) {
	index := m.activeTerminalLine()
	line := []rune(m.lines[index])
	styles := m.lineStyles[index]
	for len(styles) < len(line) {
		styles = append(styles, "")
	}
	for len(line) < m.terminalCol {
		line = append(line, ' ')
		styles = append(styles, "")
	}
	blanks := make([]rune, count)
	for i := range blanks {
		blanks[i] = ' '
	}
	line = append(line[:m.terminalCol], append(blanks, line[m.terminalCol:]...)...)
	styles = append(styles[:m.terminalCol], append(make([]string, count), styles[m.terminalCol:]...)...)
	if len(line) > m.terminalCols {
		line = line[:m.terminalCols]
		styles = styles[:min(len(styles), m.terminalCols)]
	}
	m.lines[index], m.lineStyles[index] = string(line), styles
}

func (m *model) eraseTerminalChars(count int) {
	index := m.activeTerminalLine()
	line := []rune(m.lines[index])
	styles := m.lineStyles[index]
	for len(styles) < len(line) {
		styles = append(styles, "")
	}
	for len(line) < min(m.terminalCols, m.terminalCol+count) {
		line = append(line, ' ')
		styles = append(styles, "")
	}
	for i := m.terminalCol; i < min(len(line), m.terminalCol+count); i++ {
		line[i] = ' '
		styles[i] = ""
	}
	m.lines[index], m.lineStyles[index] = string(line), styles
}

func (m *model) insertTerminalLines(count int) {
	m.ensureTerminalScreen()
	count = min(count, m.terminalRows-m.terminalRow)
	start := m.screenTop + m.terminalRow
	bottom := m.screenTop + m.terminalRows
	for range count {
		m.lines = append(m.lines[:start], append([]string{""}, m.lines[start:]...)...)
		m.lineStyles = append(m.lineStyles[:start], append([][]string{nil}, m.lineStyles[start:]...)...)
		m.lines = append(m.lines[:bottom], m.lines[bottom+1:]...)
		m.lineStyles = append(m.lineStyles[:bottom], m.lineStyles[bottom+1:]...)
	}
}

func (m *model) deleteTerminalLines(count int) {
	m.ensureTerminalScreen()
	count = min(count, m.terminalRows-m.terminalRow)
	start := m.screenTop + m.terminalRow
	bottom := m.screenTop + m.terminalRows
	for range count {
		m.lines = append(m.lines[:start], m.lines[start+1:]...)
		m.lineStyles = append(m.lineStyles[:start], m.lineStyles[start+1:]...)
		m.lines = append(m.lines[:bottom-1], append([]string{""}, m.lines[bottom-1:]...)...)
		m.lineStyles = append(m.lineStyles[:bottom-1], append([][]string{nil}, m.lineStyles[bottom-1:]...)...)
	}
}

func (m *model) scrollTerminalUp(count int) {
	row := m.terminalRow
	m.terminalRow = m.terminalRows - 1
	for range min(count, m.terminalRows) {
		m.terminalLineFeed()
	}
	m.terminalRow = row
}

func (m *model) scrollTerminalDown(count int) {
	row := m.terminalRow
	m.terminalRow = 0
	for range min(count, m.terminalRows) {
		m.terminalReverseIndex()
	}
	m.terminalRow = row
}

func (m *model) clearTerminalRow(row int) {
	index := m.screenTop + row
	m.lines[index], m.lineStyles[index] = "", nil
}

func (m *model) trimTranscript() {
	if len(m.lines) > maxTranscriptLines {
		start := len(m.lines) - maxTranscriptLines
		m.lines = append([]string(nil), m.lines[start:]...)
		if start < len(m.lineStyles) {
			m.lineStyles = append([][]string(nil), m.lineStyles[start:]...)
		} else {
			m.lineStyles = nil
		}
		m.screenTop = max(0, m.screenTop-start)
	}
}

func (m *model) View() tea.View {
	width, height := m.width, m.height
	if width < 20 {
		width = 20
	}
	if height < 12 {
		height = 12
	}
	bodyHeight := height - 1
	workbenchWidth := width
	if showSidebar(width) {
		workbenchWidth -= sidebarWidthFor(width) + 1
	}
	m.resizeTerminalScreen(max(1, workbenchWidth-4), max(1, bodyHeight-4))
	workbench := m.renderWorkbench(workbenchWidth, bodyHeight)
	body := workbench
	if showSidebar(width) {
		sidebar := m.renderSidebar(sidebarWidthFor(width), bodyHeight)
		body = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, gapStyle.Render(" "), workbench)
	}
	footer := m.renderFooter(width)
	content := strings.Join([]string{body, fitLine(footer, width)}, "\n")
	var popup string
	if m.palette {
		popup = m.renderPalettePopup(min(52, max(28, width-8)))
	} else if m.pathMode != transferNone {
		popup = m.renderPathPopup(min(64, max(32, width-8)))
	} else if m.configuration.mode != configurationNone {
		popup = m.renderConfigurationPopup(min(64, max(36, width-8)))
	}
	if popup != "" {
		popupWidth, popupHeight := lipgloss.Width(popup), lipgloss.Height(popup)
		canvas := lipgloss.NewCanvas(width, height)
		baseLayer := lipgloss.NewLayer(content)
		popupLayer := lipgloss.NewLayer(popup).
			X(max(0, (width-popupWidth)/2)).
			Y(max(0, (height-popupHeight)/2)).
			Z(1)
		canvas.Compose(lipgloss.NewCompositor(baseLayer, popupLayer))
		content = canvas.Render()
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.BackgroundColor = color.Black
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "xserial — " + m.portName
	if !m.palette && m.pathMode == transferNone && m.configuration.mode == configurationNone {
		workbenchX := 0
		if showSidebar(width) {
			workbenchX = sidebarWidthFor(width) + 1
		}
		view.OnMouse = mainMouseHandler(showSidebar(width), sidebarWidthFor(width), workbenchX, workbenchWidth, bodyHeight)
	} else if m.configuration.mode != configurationNone && popup != "" {
		popupWidth, popupHeight := lipgloss.Width(popup), lipgloss.Height(popup)
		portStart, _ := m.portWindow()
		view.OnMouse = configurationMouseHandler(
			m.configuration.mode,
			max(0, (width-popupWidth)/2),
			max(0, (height-popupHeight)/2),
			popupWidth,
			portStart,
			len(m.configuration.ports),
		)
	}
	if !m.palette && m.pathMode == transferNone && m.configuration.mode == configurationNone && m.focus == focusTerminal {
		if cursorX, cursorY, ok := m.terminalCursorPosition(workbenchWidth, bodyHeight); ok {
			workbenchX := 0
			if showSidebar(width) {
				workbenchX = sidebarWidthFor(width) + 1
			}
			view.Cursor = tea.NewCursor(workbenchX+2+cursorX, 3+cursorY)
			view.Cursor.Blink = false
		}
	}
	return view
}

func mainMouseHandler(sidebarVisible bool, sidebarWidth, workbenchX, workbenchWidth, bodyHeight int) func(tea.MouseMsg) tea.Cmd {
	return func(msg tea.MouseMsg) tea.Cmd {
		mouse := msg.Mouse()
		if _, ok := msg.(tea.MouseClickMsg); ok && mouse.Button == tea.MouseLeft && sidebarVisible && mouse.X < sidebarWidth {
			var mode configurationMode
			index := 0
			switch mouse.Y {
			case 3, 4:
				mode = configurationPort
			case 5, 6:
				mode = configurationBaud
			case 7, 8:
				mode = configurationFrame
			case 9, 10:
				mode, index = configurationFrame, 1
			case 11, 12:
				mode, index = configurationFrame, 2
			}
			if mode != configurationNone {
				return func() tea.Msg { return configurationOpenMsg{mode: mode, index: index} }
			}
		}
		if mouse.X < workbenchX || mouse.X >= workbenchX+workbenchWidth || mouse.Y < 3 || mouse.Y >= bodyHeight-1 {
			return nil
		}
		if _, ok := msg.(tea.MouseClickMsg); ok && mouse.Button == tea.MouseLeft {
			return func() tea.Msg { return terminalFocusMsg{} }
		}
		var delta terminalScrollMsg
		switch mouse.Button {
		case tea.MouseWheelUp:
			delta = 3
		case tea.MouseWheelDown:
			delta = -3
		default:
			return nil
		}
		return func() tea.Msg { return delta }
	}
}

func showSidebar(width int) bool { return width >= 60 }

func sidebarWidthFor(width int) int { return min(30, max(24, width/4)) }

func (m *model) renderSidebar(width, height int) string {
	rows := []string{
		sectionTitleStyle.Render("CONFIGURATION"),
		divider(width - 4),
	}
	rows = append(rows, m.renderSidebarField(0, "PORT", m.portName)...)
	rows = append(rows, m.renderSidebarField(1, "BAUD RATE", strconv.Itoa(m.baud))...)
	rows = append(rows, m.renderSidebarField(2, "DATA BITS", strconv.Itoa(m.dataBits))...)
	rows = append(rows, m.renderSidebarField(3, "PARITY", strings.ToUpper(m.parity))...)
	rows = append(rows, m.renderSidebarField(4, "STOP BITS", m.stopBits)...)
	content := strings.Join(rows, "\n")
	return sidebarStyle.Width(width).Height(height).Render(content)
}

func (m *model) renderSidebarField(index int, label, value string) []string {
	labelStyle := sidebarLabelStyle
	if m.focus == focusConfiguration && m.configurationFocusIndex == index && m.configuration.mode == configurationNone {
		labelStyle = sidebarFocusedLabelStyle
	}
	return []string{labelStyle.Render(label), sidebarValueStyle.Render(value)}
}

func (m *model) renderFooter(width int) string {
	if m.err != nil {
		return errorStyle.Width(width).Render(fitLine("  "+m.err.Error(), width))
	}
	connection := connectionStyle(m.connected).Render(m.connectionName())
	session := fieldValueStyle.Render(m.modeName()) +
		metaStyle.Render("  RX ") + fieldValueStyle.Render(formatBytes(m.rxBytes)) +
		metaStyle.Render("  TX ") + fieldValueStyle.Render(formatBytes(m.txBytes))
	status := metaStyle.Render(m.status)
	if m.focus == focusConfiguration && m.configuration.mode == configurationNone && !m.palette {
		hints := metaStyle.Render("j/k select  •  Enter edit  •  Esc/q terminal")
		content := "  " + connection + "  •  " + session + "  •  " + hints
		return footerStyle.Width(width).Render(fitLine(content, width))
	}
	hints := metaStyle.Render("Ctrl+P commands")
	content := "  " + connection + "  •  " + session + "  •  " + status + "  •  " + hints
	return footerStyle.Width(width).Render(fitLine(content, width))
}

func (m *model) renderWorkbench(width, height int) string {
	innerWidth := max(1, width-4)
	transcriptHeight := max(1, height-4)
	title := sectionTitleStyle.Render("TERMINAL")
	transcript := m.renderTranscript(innerWidth, transcriptHeight)
	content := strings.Join([]string{
		fitLine(title, innerWidth),
		divider(innerWidth),
		transcript,
	}, "\n")
	return workbenchStyle.Width(width).Height(height).Render(content)
}

func connectionStyle(connected bool) lipgloss.Style {
	if connected {
		return connectedStyle
	}
	return reconnectingStyle
}

func divider(width int) string {
	return dividerStyle.Render(strings.Repeat("─", max(1, width)))
}

func (m *model) modeName() string {
	return "Interactive"
}

func (m *model) connectionName() string {
	if m.connected {
		return "CONNECTED"
	}
	return "DISCONNECTED"
}

func (m *model) transcriptWidth() int {
	width := max(20, m.width)
	if showSidebar(width) {
		width -= sidebarWidthFor(width) + 1
	}
	return max(1, width-4)
}

func (m *model) transcriptHeight() int { return max(1, max(12, m.height)-5) }

func (m *model) renderTranscript(width, height int) string {
	visual, _, _ := m.terminalVisual(width)
	end := len(visual) - m.scroll
	if end < 0 {
		end = 0
	}
	start := max(0, end-height)
	visible := append([]string(nil), visual[start:end]...)
	for len(visible) < height {
		visible = append(visible, " ")
	}
	return strings.Join(visible, "\n")
}

func (m *model) visualLines(width int) []string {
	visual, _, _ := m.terminalVisual(width)
	return visual
}

func (m *model) terminalVisual(width int) (visual []string, cursorX, cursorLine int) {
	cursorLine = -1
	activeLine := m.screenTop + m.terminalRow
	for i := range m.lines {
		line := m.renderTerminalLine(i)
		wrapped := ansi.Hardwrap(line, width, false)
		wrappedLines := strings.Split(wrapped, "\n")
		if i == activeLine {
			plain := []rune(m.lines[i])
			column := min(m.terminalCol, len(plain))
			prefixLines := strings.Split(ansi.Hardwrap(string(plain[:column]), width, false), "\n")
			cursorY := len(prefixLines) - 1
			cursorX = ansi.StringWidth(prefixLines[cursorY])
			if cursorX >= width {
				cursorX = 0
				cursorY++
			}
			for len(wrappedLines) <= cursorY {
				wrappedLines = append(wrappedLines, "")
			}
			cursorLine = len(visual) + cursorY
		}
		visual = append(visual, wrappedLines...)
	}
	return visual, cursorX, cursorLine
}

func (m *model) terminalCursorPosition(workbenchWidth, bodyHeight int) (x, y int, ok bool) {
	width := max(1, workbenchWidth-4)
	height := max(1, bodyHeight-4)
	visual, cursorX, cursorLine := m.terminalVisual(width)
	if cursorLine < 0 {
		return 0, 0, false
	}
	end := max(0, len(visual)-m.scroll)
	start := max(0, end-height)
	if cursorLine < start || cursorLine >= end {
		return 0, 0, false
	}
	return cursorX, cursorLine - start, true
}

func (m *model) renderTerminalLine(index int) string {
	if index >= len(m.lineStyles) || len(m.lineStyles[index]) == 0 {
		return m.lines[index]
	}
	runes := []rune(m.lines[index])
	styles := m.lineStyles[index]
	var rendered strings.Builder
	active := ""
	for i, r := range runes {
		style := ""
		if i < len(styles) {
			style = styles[i]
		}
		if style != active {
			if active != "" {
				rendered.WriteString("\x1b[0m")
			}
			rendered.WriteString(style)
			active = style
		}
		rendered.WriteRune(r)
	}
	if active != "" {
		rendered.WriteString("\x1b[0m")
	}
	return rendered.String()
}

func (m *model) renderPalettePopup(width int) string {
	rows := []string{sectionTitleStyle.Render("Command Palette")}
	for i, command := range commands {
		prefix := "  "
		if i == m.paletteIndex {
			prefix = "› "
		}
		label := command.label
		if command.key != "" {
			label = "[" + command.key + "] " + label
		}
		if command.enabled != nil && !command.enabled(m) {
			label += " (inactive)"
		}
		row := prefix + label
		if i == m.paletteIndex {
			row = paletteSelectionStyle.Render(row)
		} else if command.enabled != nil && !command.enabled(m) {
			row = metaStyle.Render(row)
		}
		rows = append(rows, row)
	}
	rows = append(rows, "", footerStyle.Render("↑/↓ or j/k select  •  Enter run  •  Esc/q close"))
	for i := range rows {
		rows[i] = fitLine(rows[i], width-4)
	}
	return paletteStyle.Width(width).Render(strings.Join(rows, "\n"))
}

func (m *model) renderPathPopup(width int) string {
	input := promptStyle.Render("Local path › ") + string(m.input) + valueStyle.Render("▏")
	rows := []string{
		sectionTitleStyle.Render("Send raw file"),
		"",
		fitLine(inputBarStyle.Render(input), width-4),
		"",
		footerStyle.Render("Enter send  •  Esc cancel"),
	}
	return paletteStyle.Width(width).Render(strings.Join(rows, "\n"))
}

func (m *model) clampScroll() {
	maximum := max(0, len(m.visualLines(m.transcriptWidth()))-m.transcriptHeight())
	if m.scroll > maximum {
		m.scroll = maximum
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func fitLine(line string, width int) string {
	if lipgloss.Width(line) > width {
		return ansi.Truncate(line, width, "")
	}
	return line
}

func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
}

var (
	background               = lipgloss.Color("#000000")
	accent                   = lipgloss.Color("#D97757")
	accentBright             = lipgloss.Color("#E99578")
	green                    = lipgloss.Color("#34D399")
	muted                    = lipgloss.Color("#A8A29E")
	panelBorder              = accent
	red                      = lipgloss.Color("#FB7185")
	surface                  = background
	metaStyle                = lipgloss.NewStyle().Foreground(muted)
	valueStyle               = lipgloss.NewStyle().Foreground(accentBright).Bold(true)
	sectionTitleStyle        = lipgloss.NewStyle().Foreground(accentBright).Bold(true)
	sidebarLabelStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
	sidebarFocusedLabelStyle = lipgloss.NewStyle().Foreground(accentBright).Bold(true).Underline(true)
	sidebarValueStyle        = lipgloss.NewStyle().Foreground(green)
	fieldLabelStyle          = lipgloss.NewStyle().Foreground(muted).Bold(true)
	fieldValueStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("#F8FAFC"))
	connectedStyle           = lipgloss.NewStyle().Foreground(green).Bold(true)
	reconnectingStyle        = lipgloss.NewStyle().Foreground(red).Bold(true)
	dividerStyle             = lipgloss.NewStyle().Foreground(panelBorder)
	sidebarStyle             = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(panelBorder).Background(surface).Padding(0, 1)
	workbenchStyle           = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(panelBorder).Background(surface).Padding(0, 1)
	gapStyle                 = lipgloss.NewStyle().Background(background)
	inputBarStyle            = lipgloss.NewStyle().Background(surface)
	paletteStyle             = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Background(surface).Padding(1, 2)
	paletteSelectionStyle    = lipgloss.NewStyle().Foreground(accentBright).Bold(true)
	promptStyle              = lipgloss.NewStyle().Foreground(accentBright).Bold(true)
	footerStyle              = lipgloss.NewStyle().Foreground(muted).Background(background)
	errorStyle               = lipgloss.NewStyle().Foreground(red).Background(background).Bold(true)
)

var _ middleware.Frontend = (*Frontend)(nil)
