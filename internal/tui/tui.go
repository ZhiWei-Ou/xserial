package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

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
type sendResultMsg struct {
	data  []byte
	at    time.Time
	err   error
	input []rune
}

type command struct {
	label   string
	enabled func(*model) bool
	run     func(*model) tea.Cmd
}

var commands = []command{
	{label: "Send raw file", enabled: func(m *model) bool { return m.connected && !m.transferring }, run: func(m *model) tea.Cmd {
		m.pathMode, m.input, m.status = transferRawUpload, nil, "Enter a local file path for raw upload"
		return nil
	}},
	{label: "Clear traffic", run: func(m *model) tea.Cmd { m.clearTranscript(); return nil }},
	{label: "Cancel transfer", enabled: func(m *model) bool { return m.transferring }, run: func(m *model) tea.Cmd {
		m.endpoint.CancelTransfer()
		m.status = "Canceling transfer…"
		return nil
	}},
	{label: "Quit", run: func(m *model) tea.Cmd { m.endpoint.Quit(); return tea.Quit }},
}

type model struct {
	endpoint     middleware.Endpoint
	timeFormat   string
	portName     string
	baud         int
	frame        string
	width        int
	height       int
	lines        []string
	input        []rune
	pathMode     transferMode
	transferMode transferMode
	transferring bool
	connected    bool
	palette      bool
	paletteIndex int
	status       string
	scroll       int
	rxBytes      int64
	txBytes      int64
	err          error
	history      []string
	historyIndex int
}

func newModel(endpoint middleware.Endpoint, cfg Config) *model {
	return &model{
		endpoint: endpoint, timeFormat: cfg.TimeFormat, portName: cfg.PortName,
		baud: cfg.Baud, frame: cfg.Frame, connected: true, status: "Ready — enter Hex bytes",
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
		if errors.Is(msg.err, middleware.ErrDisconnected) {
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
			m.txBytes += int64(len(msg.data))
			m.appendTraffic("TX", msg.data, msg.at)
			canonical := formatHexInput(msg.data)
			if len(m.history) == 0 || m.history[len(m.history)-1] != canonical {
				m.history = append(m.history, canonical)
			}
			if len(m.history) > 100 {
				m.history = append([]string(nil), m.history[len(m.history)-100:]...)
			}
			m.historyIndex = len(m.history)
			m.status = fmt.Sprintf("Sent %d bytes", len(msg.data))
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleEvent(event middleware.Event) {
	switch event := event.(type) {
	case middleware.Disconnected:
		m.connected = false
		m.status = fmt.Sprintf("Serial port disconnected: %v — retrying…", event.Err)
	case middleware.Reconnecting:
		m.connected = false
		m.status = fmt.Sprintf("Reconnecting — attempt %d/%d", event.Attempt, event.Limit)
	case middleware.Reconnected:
		m.connected = true
		m.status = "Serial port reconnected"
	case middleware.Received:
		atBottom := m.scroll == 0
		before := len(m.visualLines(m.transcriptWidth()))
		m.appendTraffic("RX", event.Data, event.At)
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
		if len(m.history) > 0 {
			if m.historyIndex > 0 {
				m.historyIndex--
			}
			m.input = []rune(m.history[m.historyIndex])
		}
		return m, nil
	case "down":
		if m.historyIndex < len(m.history) {
			m.historyIndex++
			if m.historyIndex == len(m.history) {
				m.input = nil
			} else {
				m.input = []rune(m.history[m.historyIndex])
			}
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

func (m *model) clearTranscript() {
	m.lines = nil
	m.scroll = 0
	m.status = "Transcript cleared"
}

func (m *model) sendInput() (tea.Model, tea.Cmd) {
	if !m.connected {
		m.status = "Serial port is disconnected"
		return m, nil
	}
	originalInput := append([]rune(nil), m.input...)
	data, err := parseHexInput(string(m.input))
	if err != nil {
		m.status = fmt.Sprintf("Invalid hex: %v", err)
		return m, nil
	}
	if len(data) == 0 {
		m.status = "Nothing to send"
		return m, nil
	}
	m.input = nil
	return m, func() tea.Msg {
		err := m.endpoint.Send(context.Background(), data)
		return sendResultMsg{data: append([]byte(nil), data...), at: time.Now(), err: err, input: originalInput}
	}
}

func parseHexInput(input string) ([]byte, error) {
	fields := strings.Fields(strings.ReplaceAll(input, ",", " "))
	if len(fields) == 0 {
		return nil, nil
	}
	var data []byte
	for _, field := range fields {
		if strings.HasPrefix(strings.ToLower(field), "0x") {
			field = field[2:]
			if len(field) != 2 {
				return nil, fmt.Errorf("0x value %q must contain exactly two digits", field)
			}
		}
		if len(field)%2 != 0 {
			return nil, fmt.Errorf("%q contains an odd number of digits", field)
		}
		for i := 0; i < len(field); i += 2 {
			pair := field[i : i+2]
			value, err := strconv.ParseUint(pair, 16, 8)
			if err != nil {
				return nil, fmt.Errorf("%q is not Hex", pair)
			}
			data = append(data, byte(value))
		}
	}
	return data, nil
}

func formatHexInput(data []byte) string {
	parts := make([]string, len(data))
	for i, value := range data {
		parts[i] = fmt.Sprintf("%02X", value)
	}
	return strings.Join(parts, " ")
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

func (m *model) appendTraffic(direction string, data []byte, at time.Time) {
	for offset := 0; offset < len(data); offset += 16 {
		end := min(len(data), offset+16)
		m.lines = append(m.lines, m.linePrefix(at)+formatTrafficLine(direction, data[offset:end]))
	}
	m.trimTranscript()
}

func formatTrafficLine(direction string, data []byte) string {
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
	return fmt.Sprintf("%-2s %4d B  %s |%s|", direction, len(data), hexPart.String(), asciiPart.String())
}

func (m *model) linePrefix(at time.Time) string {
	if m.timeFormat == "" {
		return ""
	}
	return "[" + at.Format(m.timeFormat) + "] "
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
	header := titleStyle.Render("  XSERIAL") + "  " + badgeStyle.Render(m.connectionName()) +
		metaStyle.Render(fmt.Sprintf("  %s  •  %d baud  •  %s", m.portName, m.baud, m.frame))
	stats := metaStyle.Render("  RX ") + valueStyle.Render(formatBytes(m.rxBytes)) +
		metaStyle.Render("   TX ") + valueStyle.Render(formatBytes(m.txBytes)) +
		metaStyle.Render("   MODE ") + valueStyle.Render(m.modeName())

	panelContent := m.renderTranscript(m.transcriptWidthFor(width), m.transcriptHeightFor(height))
	panel := panelStyle.Width(width).Render(panelContent)
	prompt := m.modeName() + " › "
	displayInput := string(m.input)
	if m.pathMode == transferNone {
		if parsed, err := parseHexInput(displayInput); err == nil {
			displayInput = formatHexInput(parsed)
			prompt = fmt.Sprintf("Hex (%d B) › ", len(parsed))
		} else {
			prompt = "Hex (!invalid) › "
		}
	}
	if m.pathMode != transferNone {
		prompt = m.pathMode.label() + " › "
	}
	input := promptStyle.Render(prompt) + displayInput
	if !m.transferring {
		input += valueStyle.Render("▏")
	}
	status := footerStyle.Render(m.status)
	if m.err != nil {
		status = errorStyle.Render(m.err.Error())
	}
	footer := footerStyle.Render("Ctrl+P commands  •  ↑/↓ history  •  PgUp/PgDn scroll  •  Ctrl+C quit")
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
	return "Hex"
}

func (m *model) connectionName() string {
	if m.connected {
		return "CONNECTED"
	}
	return "RECONNECTING"
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

var _ middleware.Frontend = (*Frontend)(nil)
