package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ZhiWei-Ou/xserial/internal/session"
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
}

type Frontend struct{ cfg Config }

func New(cfg Config) *Frontend { return &Frontend{cfg: cfg} }

func (f *Frontend) Run(ctx context.Context, endpoint session.Endpoint) error {
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

type displayMode int

const (
	modeText displayMode = iota
	modeHex
)

type transferMode int

const (
	transferNone transferMode = iota
	transferRawUpload
	transferYMODEMUpload
	transferYMODEMDownload
)

type endpointEventMsg struct{ event session.Event }
type endpointClosedMsg struct{}
type sendResultMsg struct {
	bytes int
	err   error
	input []rune
}

type command struct {
	label   string
	enabled func(*model) bool
	run     func(*model) tea.Cmd
}

var commands = []command{
	{label: "Toggle Text / Hex mode", enabled: func(m *model) bool { return !m.transferring }, run: func(m *model) tea.Cmd { m.switchMode(); return nil }},
	{label: "Upload raw file", enabled: func(m *model) bool { return !m.transferring }, run: func(m *model) tea.Cmd {
		m.pathMode, m.input, m.status = transferRawUpload, nil, "Enter a local file path for raw upload"
		return nil
	}},
	{label: "Upload with YMODEM", enabled: func(m *model) bool { return !m.transferring }, run: func(m *model) tea.Cmd {
		m.pathMode, m.input, m.status = transferYMODEMUpload, nil, "Enter a local file path for YMODEM upload"
		return nil
	}},
	{label: "Download with YMODEM", enabled: func(m *model) bool { return !m.transferring }, run: func(m *model) tea.Cmd {
		m.input = nil
		if err := m.endpoint.StartYMODEMDownload(context.Background(), "."); err != nil {
			m.status = fmt.Sprintf("YMODEM download failed: %v", err)
			return nil
		}
		m.transferMode, m.transferring = transferYMODEMDownload, true
		m.status = "Waiting for YMODEM sender…"
		return nil
	}},
	{label: "Clear transcript", run: func(m *model) tea.Cmd { m.clearTranscript(); return nil }},
	{label: "Cancel transfer", enabled: func(m *model) bool { return m.transferring }, run: func(m *model) tea.Cmd {
		m.endpoint.CancelTransfer()
		m.status = "Canceling transfer…"
		return nil
	}},
	{label: "Quit", run: func(m *model) tea.Cmd { m.endpoint.Quit(); return tea.Quit }},
}

type model struct {
	endpoint     session.Endpoint
	timeFormat   string
	portName     string
	baud         int
	frame        string
	width        int
	height       int
	lines        []string
	currentLine  string
	currentAt    time.Time
	mode         displayMode
	input        []rune
	pathMode     transferMode
	transferMode transferMode
	transferring bool
	palette      bool
	paletteIndex int
	status       string
	scroll       int
	rxBytes      int64
	txBytes      int64
	err          error
	pendingCR    bool

	escapeKind byte
	escapeBuf  []byte
	activeSGR  string
	hexBytes   []byte
	hexOffset  int64
}

func newModel(endpoint session.Endpoint, cfg Config) *model {
	return &model{
		endpoint: endpoint, timeFormat: cfg.TimeFormat, portName: cfg.PortName,
		baud: cfg.Baud, frame: cfg.Frame, status: "Ready — Text mode",
	}
}

func (m *model) Init() tea.Cmd { return waitEvent(m.endpoint.Events()) }

func waitEvent(events <-chan session.Event) tea.Cmd {
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
		if errors.Is(msg.err, session.ErrDisconnected) {
			if len(m.input) == 0 {
				m.input = append([]rune(nil), msg.input...)
			}
			m.status = "Serial port disconnected; waiting to reconnect"
		} else if msg.err != nil {
			if len(m.input) == 0 {
				m.input = append([]rune(nil), msg.input...)
			}
			m.status = fmt.Sprintf("Send failed: %v", msg.err)
		} else {
			m.txBytes += int64(msg.bytes)
			m.status = fmt.Sprintf("Sent %d bytes", msg.bytes)
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleEvent(event session.Event) {
	switch event := event.(type) {
	case session.Disconnected:
		m.status = fmt.Sprintf("Serial port disconnected: %v — retrying…", event.Err)
	case session.Reconnected:
		m.status = "Serial port reconnected"
	case session.Received:
		atBottom := m.scroll == 0
		before := len(m.visualLines(m.transcriptWidth()))
		if m.mode == modeHex {
			m.appendHex(event.Data, event.At)
		} else {
			m.appendText(event.Data, event.At)
		}
		m.rxBytes += int64(len(event.Data))
		m.trimTranscript()
		if !atBottom {
			after := len(m.visualLines(m.transcriptWidth()))
			m.scroll += after - before
		}
		m.clampScroll()
	case session.UploadStarted:
		m.transferMode, m.transferring = transferRawUpload, true
		m.status = fmt.Sprintf("Uploading %s…", event.Path)
	case session.UploadProgress:
		m.status = fmt.Sprintf("Uploading %s — %s / %s", event.Path, formatBytes(event.Written), formatBytes(event.Total))
	case session.UploadFinished:
		m.transferMode, m.transferring = transferNone, false
		if event.Err != nil {
			m.status = fmt.Sprintf("Upload failed: %v", event.Err)
		} else {
			m.txBytes += event.Bytes
			m.status = fmt.Sprintf("Uploaded %s (%s)", event.Path, formatBytes(event.Bytes))
		}
		event.Acknowledge()
	case session.YMODEMProgress:
		m.transferring = true
		if event.Direction == "download" {
			m.transferMode = transferYMODEMDownload
		} else {
			m.transferMode = transferYMODEMUpload
		}
		m.status = fmt.Sprintf("YMODEM %s — %s / %s", event.Direction, formatBytes(event.Written), formatBytes(event.Total))
	case session.YMODEMFrameRetry:
		m.status = fmt.Sprintf("YMODEM %s retry — block %d, attempt %d: %s", event.Direction, event.Block, event.Attempt, event.Reason)
		event.Acknowledge()
	case session.YMODEMFinished:
		m.transferMode, m.transferring = transferNone, false
		stats := fmt.Sprintf(
			"CRC32 %08x — failed %d, retried %d",
			event.CRC32, event.FailedFrames, event.RetriedFrames,
		)
		if errors.Is(event.Err, context.Canceled) {
			m.status = fmt.Sprintf("YMODEM %s canceled — %s", event.Direction, stats)
		} else if event.Err != nil {
			m.status = fmt.Sprintf("YMODEM %s failed: %v — %s", event.Direction, event.Err, stats)
		} else {
			if event.Direction == "download" {
				m.rxBytes += event.Bytes
			} else {
				m.txBytes += event.Bytes
			}
			m.status = fmt.Sprintf("YMODEM %s complete — %s (%s) — %s", event.Direction, event.Path, formatBytes(event.Bytes), stats)
		}
		event.Acknowledge()
	}
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Keystroke()
	if key == "ctrl+c" {
		m.endpoint.Quit()
		return m, tea.Quit
	}
	if key == "ctrl+p" {
		m.palette = !m.palette
		m.paletteIndex = 0
		return m, nil
	}
	if m.palette {
		return m.handlePalette(key)
	}
	if key == "esc" && m.pathMode != transferNone {
		m.pathMode = transferNone
		m.input = nil
		m.status = "File selection canceled"
		return m, nil
	}
	if key == "esc" && m.transferring {
		m.endpoint.CancelTransfer()
		m.status = "Canceling transfer…"
		return m, nil
	}
	switch key {
	case "up":
		m.scroll++
		m.clampScroll()
		return m, nil
	case "down":
		if m.scroll > 0 {
			m.scroll--
		}
		return m, nil
	case "pgup":
		m.scroll += max(1, m.transcriptHeight()-1)
		m.clampScroll()
		return m, nil
	case "pgdown":
		m.scroll -= max(1, m.transcriptHeight()-1)
		if m.scroll < 0 {
			m.scroll = 0
		}
		return m, nil
	case "backspace":
		if len(m.input) > 0 && !m.transferring {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case "enter":
		if m.transferring {
			return m, nil
		}
		if m.pathMode != transferNone {
			return m.startFileTransfer()
		}
		return m.sendInput()
	}
	if msg.Key().Text != "" && !m.transferring {
		m.input = append(m.input, []rune(msg.Key().Text)...)
	}
	return m, nil
}

func (m *model) handlePalette(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "ctrl+p":
		m.palette = false
	case "up":
		m.paletteIndex = (m.paletteIndex - 1 + len(commands)) % len(commands)
	case "down":
		m.paletteIndex = (m.paletteIndex + 1) % len(commands)
	case "enter":
		cmd := commands[m.paletteIndex]
		if cmd.enabled != nil && !cmd.enabled(m) {
			m.status = cmd.label + " is unavailable"
			return m, nil
		}
		m.palette = false
		return m, cmd.run(m)
	}
	return m, nil
}

func (m *model) switchMode() {
	m.finishCurrent()
	m.activeSGR = ""
	m.resetEscape()
	m.pendingCR = false
	if m.mode == modeText {
		m.mode = modeHex
		m.status = "Hex mode — enter two-digit bytes separated by spaces"
	} else {
		m.mode = modeText
		m.status = "Text mode"
	}
	m.input = nil
}

func (m *model) clearTranscript() {
	m.lines, m.currentLine, m.hexBytes = nil, "", nil
	m.activeSGR = ""
	m.resetEscape()
	m.pendingCR = false
	m.scroll = 0
	m.status = "Transcript cleared"
}

func (m *model) sendInput() (tea.Model, tea.Cmd) {
	originalInput := append([]rune(nil), m.input...)
	var data []byte
	if m.mode == modeHex {
		parsed, err := parseHexInput(string(m.input))
		if err != nil {
			m.status = fmt.Sprintf("Invalid hex: %v", err)
			return m, nil
		}
		data = parsed
	} else {
		data = append([]byte(string(m.input)), '\r')
	}
	if len(data) == 0 {
		m.status = "Nothing to send"
		return m, nil
	}
	m.input = nil
	return m, func() tea.Msg {
		err := m.endpoint.Send(context.Background(), data)
		return sendResultMsg{bytes: len(data), err: err, input: originalInput}
	}
}

func parseHexInput(input string) ([]byte, error) {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return nil, nil
	}
	data := make([]byte, len(fields))
	for i, field := range fields {
		if len(field) != 2 {
			return nil, fmt.Errorf("%q must contain exactly two digits", field)
		}
		value, err := strconv.ParseUint(field, 16, 8)
		if err != nil {
			return nil, fmt.Errorf("%q is not a byte", field)
		}
		data[i] = byte(value)
	}
	return data, nil
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
	var err error
	if mode == transferYMODEMUpload {
		err = m.endpoint.StartYMODEMUpload(context.Background(), path)
	} else {
		err = m.endpoint.StartUpload(context.Background(), path)
	}
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
	case transferYMODEMUpload:
		return "YMODEM upload"
	case transferYMODEMDownload:
		return "YMODEM download"
	default:
		return "transfer"
	}
}

func (m *model) appendText(data []byte, at time.Time) {
	if m.currentLine == "" {
		m.currentAt = at
		m.currentLine = m.linePrefix(at) + m.activeSGR
	}
	for _, b := range data {
		if m.pendingCR {
			m.pendingCR = false
			if b == '\n' {
				m.finishCurrent()
				continue
			}
			m.resetCurrentLine(at)
		}
		if m.consumeEscape(b) {
			continue
		}
		switch b {
		case '\n':
			m.finishCurrent()
		case '\r':
			m.pendingCR = true
		case '\t':
			m.currentLine += "    "
		default:
			if b >= 0x20 && b != 0x7f {
				m.currentLine += string([]byte{b})
			}
		}
	}
}

func (m *model) resetCurrentLine(at time.Time) {
	m.currentAt = at
	m.currentLine = m.linePrefix(at) + m.activeSGR
}

func (m *model) consumeEscape(b byte) bool {
	if m.escapeKind == 0 {
		if b == 0x1b {
			m.escapeKind = 'e'
			m.escapeBuf = []byte{b}
			return true
		}
		return false
	}
	m.escapeBuf = append(m.escapeBuf, b)
	switch m.escapeKind {
	case 'e':
		switch b {
		case '[':
			m.escapeKind = '['
		case ']', 'P', '_', '^', 'X':
			m.escapeKind = b
		default:
			m.resetEscape()
		}
	case '[':
		if b >= 0x40 && b <= 0x7e {
			if b == 'm' && validSGR(m.escapeBuf) {
				seq := string(m.escapeBuf)
				m.currentLine += seq
				if seq == "\x1b[m" || seq == "\x1b[0m" {
					m.activeSGR = ""
				} else {
					m.activeSGR += seq
				}
			} else if b == 'K' && erasesWholeLine(m.escapeBuf) {
				m.resetCurrentLine(m.currentAt)
			}
			m.resetEscape()
		}
	case ']', 'P', '_', '^', 'X':
		if (m.escapeKind == ']' && b == 0x07) || (len(m.escapeBuf) >= 2 && m.escapeBuf[len(m.escapeBuf)-2] == 0x1b && b == '\\') {
			m.resetEscape()
		}
	}
	if len(m.escapeBuf) > 1024 {
		m.resetEscape()
	}
	return true
}

func erasesWholeLine(seq []byte) bool {
	return bytes.Equal(seq, []byte("\x1b[2K"))
}

func validSGR(seq []byte) bool {
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' || seq[len(seq)-1] != 'm' {
		return false
	}
	for _, b := range seq[2 : len(seq)-1] {
		if (b < '0' || b > '9') && b != ';' && b != ':' {
			return false
		}
	}
	return true
}

func (m *model) resetEscape() { m.escapeKind, m.escapeBuf = 0, nil }

func (m *model) appendHex(data []byte, at time.Time) {
	for i, b := range data {
		if len(m.hexBytes) == 0 {
			m.hexOffset = m.rxBytes + int64(i)
			m.currentAt = at
		}
		m.hexBytes = append(m.hexBytes, b)
		m.currentLine = m.linePrefix(m.currentAt) + formatHexLine(m.hexOffset, m.hexBytes)
		if len(m.hexBytes) == 16 {
			m.finishCurrent()
			m.hexBytes = nil
		}
	}
}

func formatHexLine(offset int64, data []byte) string {
	var hexPart strings.Builder
	var asciiPart strings.Builder
	for i := 0; i < 16; i++ {
		if i < len(data) {
			fmt.Fprintf(&hexPart, "%02X ", data[i])
			if data[i] >= 0x20 && data[i] <= 0x7e {
				asciiPart.WriteByte(data[i])
			} else {
				asciiPart.WriteByte('.')
			}
		} else {
			hexPart.WriteString("   ")
			asciiPart.WriteByte(' ')
		}
		if i == 7 {
			hexPart.WriteByte(' ')
		}
	}
	return fmt.Sprintf("%08X  %s |%s|", offset, hexPart.String(), asciiPart.String())
}

func (m *model) linePrefix(at time.Time) string {
	if m.timeFormat == "" {
		return ""
	}
	return "[" + at.Format(m.timeFormat) + "] "
}

func (m *model) finishCurrent() {
	if m.currentLine != "" {
		m.lines = append(m.lines, m.currentLine+"\x1b[0m")
	}
	m.currentLine = ""
	m.hexBytes = nil
}

func (m *model) trimTranscript() {
	if len(m.lines) > maxTranscriptLines {
		m.lines = append([]string(nil), m.lines[len(m.lines)-maxTranscriptLines:]...)
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
	header := titleStyle.Render("  XSERIAL") + "  " + badgeStyle.Render("CONNECTED") +
		metaStyle.Render(fmt.Sprintf("  %s  •  %d baud  •  %s", m.portName, m.baud, m.frame))
	stats := metaStyle.Render("  RX ") + valueStyle.Render(formatBytes(m.rxBytes)) +
		metaStyle.Render("   TX ") + valueStyle.Render(formatBytes(m.txBytes)) +
		metaStyle.Render("   MODE ") + valueStyle.Render(m.modeName())

	panelContent := m.renderTranscript(m.transcriptWidthFor(width), m.transcriptHeightFor(height))
	panel := panelStyle.Width(width).Render(panelContent)
	prompt := m.modeName() + " › "
	if m.pathMode != transferNone {
		prompt = m.pathMode.label() + " › "
	}
	input := promptStyle.Render(prompt) + string(m.input)
	if !m.transferring {
		input += valueStyle.Render("▏")
	}
	status := footerStyle.Render(m.status)
	if m.err != nil {
		status = errorStyle.Render(m.err.Error())
	}
	footer := footerStyle.Render("Ctrl+P commands  •  ↑/↓ scroll  •  Ctrl+C quit")
	content := strings.Join([]string{
		fitLine(header, width), fitLine(stats, width), panel,
		fitLine(input, width), fitLine(status, width), fitLine(footer, width),
	}, "\n")
	if m.palette {
		popup := m.renderPalettePopup(min(52, max(28, width-8)))
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
	view.WindowTitle = "xserial — " + m.portName
	return view
}

func (m *model) modeName() string {
	if m.mode == modeHex {
		return "Hex"
	}
	return "Text"
}

func (m *model) transcriptWidth() int               { return m.transcriptWidthFor(max(20, m.width)) }
func (m *model) transcriptHeight() int              { return m.transcriptHeightFor(max(12, m.height)) }
func (m *model) transcriptWidthFor(width int) int   { return max(1, width-6) }
func (m *model) transcriptHeightFor(height int) int { return max(1, height-7) }

func (m *model) renderTranscript(width, height int) string {
	visual := m.visualLines(width)
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
	logical := append([]string(nil), m.lines...)
	if m.currentLine != "" {
		logical = append(logical, m.currentLine+"\x1b[0m")
	}
	var visual []string
	for _, line := range logical {
		wrapped := ansi.Hardwrap(line, width, false)
		visual = append(visual, strings.Split(wrapped, "\n")...)
	}
	return visual
}

func (m *model) renderPalettePopup(width int) string {
	rows := []string{titleStyle.Render("Command Palette")}
	for i, command := range commands {
		prefix := "  "
		if i == m.paletteIndex {
			prefix = "› "
		}
		label := command.label
		if command.enabled != nil && !command.enabled(m) {
			label += " (inactive)"
		}
		rows = append(rows, prefix+label)
	}
	rows = append(rows, "", footerStyle.Render("↑/↓ select  •  Enter run  •  Esc close"))
	for i := range rows {
		rows[i] = fitLine(rows[i], width-4)
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
	accent       = lipgloss.Color("#8B5CF6")
	accentBright = lipgloss.Color("#C4B5FD")
	green        = lipgloss.Color("#34D399")
	muted        = lipgloss.Color("#94A3B8")
	panelBorder  = lipgloss.Color("#475569")
	red          = lipgloss.Color("#FB7185")
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	badgeStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#0F172A")).Background(green).Padding(0, 1)
	metaStyle    = lipgloss.NewStyle().Foreground(muted)
	valueStyle   = lipgloss.NewStyle().Foreground(accentBright).Bold(true)
	panelStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(panelBorder).Padding(0, 1)
	paletteStyle = lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(accent).Background(lipgloss.Color("#111827")).Padding(1, 2)
	promptStyle  = lipgloss.NewStyle().Foreground(accentBright).Bold(true)
	footerStyle  = lipgloss.NewStyle().Foreground(muted)
	errorStyle   = lipgloss.NewStyle().Foreground(red).Bold(true)
)

var _ session.Frontend = (*Frontend)(nil)
