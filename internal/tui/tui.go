package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ZhiWei-Ou/xserial/internal/session"
	"github.com/charmbracelet/x/ansi"
)

const maxTranscriptLines = 5000

type SerialPort interface {
	io.ReadWriteCloser
}

type Config struct {
	Port       SerialPort
	Input      io.Reader
	Output     io.Writer
	ReceiveLog io.Writer
	TimeFormat string
	PortName   string
	Baud       int
	Frame      string
}

type serialDataMsg struct {
	data []byte
	at   time.Time
}

type serialErrorMsg struct {
	err error
}

type uploadResultMsg struct {
	path  string
	bytes int64
	err   error
}

type tickMsg struct{}

type model struct {
	ctx           context.Context
	port          SerialPort
	receiveLog    io.Writer
	timeFormat    string
	portName      string
	baud          int
	frame         string
	width         int
	height        int
	lines         []string
	currentLine   string
	displayStart  bool
	logLineStart  bool
	input         []rune
	uploadMode    bool
	uploading     bool
	uploadCancel  context.CancelFunc
	status        string
	scroll        int
	rxBytes       int64
	txBytes       int64
	startedAt     time.Time
	quitRequested bool
	err           error
}

var (
	accent       = lipgloss.Color("#8B5CF6")
	accentBright = lipgloss.Color("#C4B5FD")
	green        = lipgloss.Color("#34D399")
	muted        = lipgloss.Color("#94A3B8")
	panelBorder  = lipgloss.Color("#475569")
	red          = lipgloss.Color("#FB7185")

	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	badgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#0F172A")).
			Background(green).
			Padding(0, 1)
	metaStyle   = lipgloss.NewStyle().Foreground(muted)
	valueStyle  = lipgloss.NewStyle().Foreground(accentBright).Bold(true)
	panelStyle  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(panelBorder).Padding(0, 1)
	promptStyle = lipgloss.NewStyle().Foreground(accentBright).Bold(true)
	footerStyle = lipgloss.NewStyle().Foreground(muted)
	errorStyle  = lipgloss.NewStyle().Foreground(red).Bold(true)
)

func Run(ctx context.Context, cfg Config) error {
	if cfg.Port == nil {
		return errors.New("serial port is nil")
	}
	if cfg.Input == nil {
		return errors.New("TUI input is nil")
	}
	if cfg.Output == nil {
		return errors.New("TUI output is nil")
	}

	m := newModel(ctx, cfg)
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(cfg.Input), tea.WithOutput(cfg.Output))
	readDone := make(chan error, 1)
	go readSerial(program, cfg.Port, readDone)

	finalModel, runErr := program.Run()
	_ = cfg.Port.Close()
	readErr := <-readDone

	if final, ok := finalModel.(*model); ok {
		if final.err != nil {
			return final.err
		}
		if final.quitRequested {
			return nil
		}
	}
	if runErr != nil {
		if ctx.Err() != nil || errors.Is(runErr, tea.ErrInterrupted) || errors.Is(runErr, tea.ErrProgramKilled) {
			return nil
		}
		return runErr
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) && ctx.Err() == nil {
		return readErr
	}
	return nil
}

func newModel(ctx context.Context, cfg Config) *model {
	return &model{
		ctx:          ctx,
		port:         cfg.Port,
		receiveLog:   cfg.ReceiveLog,
		timeFormat:   cfg.TimeFormat,
		portName:     cfg.PortName,
		baud:         cfg.Baud,
		frame:        cfg.Frame,
		displayStart: true,
		logLineStart: true,
		status:       "Ready — type a command and press Enter",
		startedAt:    time.Now(),
	}
}

func readSerial(program *tea.Program, port io.Reader, done chan<- error) {
	buf := make([]byte, 4096)
	for {
		n, err := port.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			program.Send(serialDataMsg{data: data, at: time.Now()})
		}
		if err != nil {
			program.Send(serialErrorMsg{err: err})
			done <- err
			return
		}
	}
}

func (m *model) Init() tea.Cmd {
	return tick()
}

func (m *model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case serialDataMsg:
		m.rxBytes += int64(len(msg.data))
		if err := m.record(msg.data, msg.at); err != nil {
			m.err = err
			return m, tea.Quit
		}
		m.appendReceived(msg.data, msg.at)
	case serialErrorMsg:
		if msg.err != nil && !errors.Is(msg.err, io.EOF) {
			m.err = fmt.Errorf("read serial port: %w", msg.err)
		}
		return m, tea.Quit
	case uploadResultMsg:
		m.uploading = false
		m.uploadCancel = nil
		if msg.err != nil {
			m.status = fmt.Sprintf("Upload failed: %v", msg.err)
		} else {
			m.txBytes += msg.bytes
			m.status = fmt.Sprintf("Uploaded %s (%s)", msg.path, formatBytes(msg.bytes))
		}
	case tickMsg:
		return m, tick()
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Keystroke()
	switch key {
	case "ctrl+c":
		if m.uploadCancel != nil {
			m.uploadCancel()
		}
		m.quitRequested = true
		return m, tea.Quit
	case "ctrl+l":
		m.lines = nil
		m.currentLine = ""
		m.displayStart = true
		m.scroll = 0
		m.status = "Transcript cleared"
		return m, nil
	case "ctrl+u":
		if !m.uploading {
			m.uploadMode = true
			m.input = nil
			m.status = "Enter a local file path"
		}
		return m, nil
	case "esc":
		if m.uploadMode {
			m.uploadMode = false
			m.input = nil
			m.status = "Upload canceled"
		}
		return m, nil
	case "pgup", "up":
		m.scroll += 3
		return m, nil
	case "pgdown", "down":
		m.scroll -= 3
		if m.scroll < 0 {
			m.scroll = 0
		}
		return m, nil
	case "backspace":
		if len(m.input) > 0 && !m.uploading {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case "enter":
		if m.uploading {
			return m, nil
		}
		if m.uploadMode {
			return m.startUpload()
		}
		return m.sendInput()
	}

	if msg.Key().Text != "" && !m.uploading {
		m.input = append(m.input, []rune(msg.Key().Text)...)
	}
	return m, nil
}

func (m *model) sendInput() (tea.Model, tea.Cmd) {
	data := append([]byte(string(m.input)), '\r')
	if err := writeFull(m.port, data); err != nil {
		m.err = fmt.Errorf("write serial port: %w", err)
		return m, tea.Quit
	}
	m.txBytes += int64(len(data))
	m.input = nil
	m.status = fmt.Sprintf("Sent %d bytes", len(data))
	return m, nil
}

func (m *model) startUpload() (tea.Model, tea.Cmd) {
	path := strings.TrimSpace(string(m.input))
	if path == "" {
		m.uploadMode = false
		m.status = "Upload canceled"
		return m, nil
	}

	uploadCtx, cancel := context.WithCancel(m.ctx)
	m.uploadCancel = cancel
	m.uploadMode = false
	m.uploading = true
	m.input = nil
	m.status = fmt.Sprintf("Uploading %s…", path)
	return m, func() tea.Msg {
		n, err := session.UploadRawFile(uploadCtx, path, m.port, nil)
		return uploadResultMsg{path: path, bytes: n, err: err}
	}
}

func (m *model) record(data []byte, at time.Time) error {
	if m.receiveLog == nil {
		return nil
	}
	if m.timeFormat == "" {
		return writeFull(m.receiveLog, data)
	}

	var output []byte
	for _, b := range data {
		if m.logLineStart {
			output = append(output, []byte("["+at.Format(m.timeFormat)+"] ")...)
			m.logLineStart = false
		}
		output = append(output, b)
		if b == '\n' {
			m.logLineStart = true
		}
	}
	if err := writeFull(m.receiveLog, output); err != nil {
		return fmt.Errorf("write receive log: %w", err)
	}
	return nil
}

func (m *model) appendReceived(data []byte, at time.Time) {
	text := ansi.Strip(string(data))
	for len(text) > 0 {
		r, size := utf8.DecodeRuneInString(text)
		text = text[size:]

		if m.displayStart {
			if m.timeFormat != "" {
				m.currentLine = "[" + at.Format(m.timeFormat) + "] "
			}
			m.displayStart = false
		}

		switch r {
		case '\n':
			m.lines = append(m.lines, m.currentLine)
			m.currentLine = ""
			m.displayStart = true
		case '\r':
		case '\t':
			m.currentLine += "    "
		default:
			if r >= 0x20 && r != 0x7f {
				m.currentLine += string(r)
			}
		}
	}

	if len(m.lines) > maxTranscriptLines {
		m.lines = append([]string(nil), m.lines[len(m.lines)-maxTranscriptLines:]...)
	}
}

func (m *model) View() tea.View {
	width := m.width
	if width < 48 {
		width = 48
	}
	height := m.height
	if height < 14 {
		height = 14
	}

	header := titleStyle.Render("  XSERIAL") + "  " + badgeStyle.Render("CONNECTED") +
		metaStyle.Render(fmt.Sprintf("  %s  •  %d baud  •  %s", m.portName, m.baud, m.frame))
	stats := metaStyle.Render("  RX ") + valueStyle.Render(formatBytes(m.rxBytes)) +
		metaStyle.Render("   TX ") + valueStyle.Render(formatBytes(m.txBytes)) +
		metaStyle.Render("   UPTIME ") + valueStyle.Render(formatDuration(time.Since(m.startedAt))) +
		metaStyle.Render("   LOG ") + valueStyle.Render(logStatus(m.receiveLog != nil))

	panelHeight := height - 9
	transcript := m.renderTranscript(width-6, panelHeight)
	panel := panelStyle.Width(width - 4).Height(panelHeight).Render(transcript)

	prompt := "Send › "
	if m.uploadMode {
		prompt = "Upload › "
	}
	input := promptStyle.Render(prompt) + string(m.input)
	if !m.uploading {
		input += valueStyle.Render("▏")
	}

	status := footerStyle.Render(m.status)
	if m.err != nil {
		status = errorStyle.Render(m.err.Error())
	}
	footer := footerStyle.Render("Enter send  •  Ctrl+U upload  •  ↑/↓ scroll  •  Ctrl+L clear  •  Ctrl+C quit")
	content := strings.Join([]string{header, stats, panel, input, status, footer}, "\n")

	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "xserial — " + m.portName
	return view
}

func (m *model) renderTranscript(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}

	logical := append([]string(nil), m.lines...)
	logical = append(logical, m.currentLine)
	var visual []string
	for _, line := range logical {
		visual = append(visual, wrapLine(line, width)...)
	}

	end := len(visual) - m.scroll
	if end < 0 {
		end = 0
	}
	start := end - height
	if start < 0 {
		start = 0
	}
	return strings.Join(visual[start:end], "\n")
}

func wrapLine(line string, width int) []string {
	runes := []rune(line)
	if len(runes) == 0 {
		return []string{""}
	}
	var lines []string
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	return append(lines, string(runes))
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func writeFull(dst io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := dst.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
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

func formatDuration(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d < time.Minute {
		return d.String()
	}
	return fmt.Sprintf("%02d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
}

func logStatus(enabled bool) string {
	if enabled {
		return "ON"
	}
	return "OFF"
}
