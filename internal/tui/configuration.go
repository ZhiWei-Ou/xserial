package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/charmbracelet/x/ansi"
)

const (
	portRefreshInterval = time.Second
	maxVisiblePorts     = 8
)

type configurationMode int

const (
	configurationNone configurationMode = iota
	configurationPort
	configurationBaud
	configurationFrame
)

type focusTarget int

const (
	focusTerminal focusTarget = iota
	focusConfiguration
)

const configurationFieldCount = 5

type configurationState struct {
	mode                  configurationMode
	draft                 middleware.ConnectionConfig
	previousStatus        string
	returnToConfiguration bool
	ports                 []PortOption
	index                 int
	input                 []rune
	loading               bool
	applying              bool
	err                   error
}

type portsLoadedMsg struct {
	ports []PortOption
	err   error
}

type refreshPortsMsg struct{}

type configurationAppliedMsg struct {
	config middleware.ConnectionConfig
	err    error
}

type configurableEndpoint interface {
	Configure(context.Context, middleware.ConnectionConfig) error
}

func connectionFrame(cfg Config) (dataBits int, parity, stopBits string) {
	dataBits, parity, stopBits = 8, "none", "1"
	if fields := strings.Split(cfg.Frame, ","); len(fields) == 3 {
		if parsed, err := strconv.Atoi(strings.TrimSpace(fields[0])); err == nil {
			dataBits = parsed
		}
		parity = parityName(fields[1])
		stopBits = strings.TrimSpace(fields[2])
	}
	if cfg.DataBits > 0 {
		dataBits = cfg.DataBits
	}
	if cfg.Parity != "" {
		parity = parityName(cfg.Parity)
	}
	if cfg.StopBits != "" {
		stopBits = cfg.StopBits
	}
	return dataBits, parity, stopBits
}

func parityName(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "N", "NONE":
		return "none"
	case "O", "ODD":
		return "odd"
	case "E", "EVEN":
		return "even"
	case "M", "MARK":
		return "mark"
	case "S", "SPACE":
		return "space"
	default:
		return "none"
	}
}

func parityCode(value string) string {
	switch parityName(value) {
	case "odd":
		return "O"
	case "even":
		return "E"
	case "mark":
		return "M"
	case "space":
		return "S"
	default:
		return "N"
	}
}

func (m *model) currentConnectionConfig() middleware.ConnectionConfig {
	return middleware.ConnectionConfig{
		PortName: m.portName,
		BaudRate: m.baud,
		DataBits: m.dataBits,
		Parity:   m.parity,
		StopBits: m.stopBits,
	}
}

func (m *model) openConfiguration(mode configurationMode) tea.Cmd {
	if _, ok := m.endpoint.(configurableEndpoint); !ok {
		m.status = "This session does not support runtime configuration"
		return nil
	}
	m.configuration = configurationState{mode: mode, draft: m.currentConnectionConfig(), previousStatus: m.status}
	switch mode {
	case configurationPort:
		if m.listPorts == nil {
			m.configuration = configurationState{}
			m.status = "Serial port discovery is unavailable"
			return nil
		}
		m.configuration.loading = true
		m.status = "Refreshing serial ports…"
		return m.loadPorts()
	case configurationBaud:
		m.configuration.input = []rune(strconv.Itoa(m.baud))
	case configurationFrame:
		m.configuration.index = 0
	}
	return nil
}

func (m *model) loadPorts() tea.Cmd {
	list := m.listPorts
	return func() tea.Msg {
		ports, err := list()
		return portsLoadedMsg{ports: ports, err: err}
	}
}

func refreshPorts() tea.Cmd {
	return tea.Tick(portRefreshInterval, func(time.Time) tea.Msg { return refreshPortsMsg{} })
}

func (m *model) handlePortsLoaded(msg portsLoadedMsg) tea.Cmd {
	if m.configuration.mode != configurationPort {
		return nil
	}
	m.configuration.loading = false
	if msg.err != nil {
		m.configuration.err = msg.err
		m.status = fmt.Sprintf("Refresh serial ports failed: %v", msg.err)
		return refreshPorts()
	}
	sort.Slice(msg.ports, func(i, j int) bool { return msg.ports[i].Name < msg.ports[j].Name })
	selected := m.configuration.draft.PortName
	if m.configuration.index < len(m.configuration.ports) {
		selected = m.configuration.ports[m.configuration.index].Name
	}
	m.configuration.ports = msg.ports
	m.configuration.index = 0
	for i, port := range msg.ports {
		if port.Name == selected {
			m.configuration.index = i
			break
		}
	}
	m.configuration.err = nil
	m.status = fmt.Sprintf("Found %d serial ports", len(msg.ports))
	return refreshPorts()
}

func (m *model) handleConfigurationKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.configuration.applying {
		return m, nil
	}
	key := msg.Keystroke()
	if key == "esc" || key == "q" || key == "ctrl+p" {
		previousStatus := m.configuration.previousStatus
		returnToConfiguration := m.configuration.returnToConfiguration
		m.configuration = configurationState{}
		m.status = previousStatus
		if returnToConfiguration {
			m.focus = focusConfiguration
		}
		return m, nil
	}
	switch m.configuration.mode {
	case configurationPort:
		return m, m.handlePortConfigurationKey(key)
	case configurationBaud:
		return m.handleBaudConfigurationKey(msg)
	case configurationFrame:
		return m.handleFrameConfigurationKey(key)
	}
	return m, nil
}

func (m *model) handleConfigurationFocusKey(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+p" {
		m.focus = focusTerminal
		m.palette = true
		m.paletteIndex = 0
		return m, nil
	}
	key = navigationKey(key)
	switch key {
	case "esc", "q":
		m.focus = focusTerminal
	case "up":
		m.configurationFocusIndex = (m.configurationFocusIndex - 1 + configurationFieldCount) % configurationFieldCount
	case "down":
		m.configurationFocusIndex = (m.configurationFocusIndex + 1) % configurationFieldCount
	case "enter":
		mode, index := focusedConfigurationTarget(m.configurationFocusIndex)
		command := m.openConfiguration(mode)
		if mode == configurationFrame {
			m.configuration.index = index
		}
		if m.configuration.mode != configurationNone {
			m.configuration.returnToConfiguration = true
		}
		return m, command
	}
	return m, nil
}

func focusedConfigurationTarget(index int) (configurationMode, int) {
	switch index {
	case 0:
		return configurationPort, 0
	case 1:
		return configurationBaud, 0
	case 2:
		return configurationFrame, 0
	case 3:
		return configurationFrame, 1
	default:
		return configurationFrame, 2
	}
}

func (m *model) handlePortConfigurationKey(key string) tea.Cmd {
	key = navigationKey(key)
	count := len(m.configuration.ports)
	if count == 0 {
		return nil
	}
	switch key {
	case "up":
		m.configuration.index = (m.configuration.index - 1 + count) % count
	case "down":
		m.configuration.index = (m.configuration.index + 1) % count
	case "enter":
		m.configuration.draft.PortName = m.configuration.ports[m.configuration.index].Name
		return m.applyConfiguration()
	}
	return nil
}

func (m *model) handleBaudConfigurationKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "backspace":
		if len(m.configuration.input) > 0 {
			m.configuration.input = m.configuration.input[:len(m.configuration.input)-1]
		}
	case "enter":
		baud, err := strconv.Atoi(string(m.configuration.input))
		if err != nil || baud <= 0 {
			m.configuration.err = errors.New("baud rate must be a positive integer")
			return m, nil
		}
		m.configuration.draft.BaudRate = baud
		return m, m.applyConfiguration()
	default:
		for _, r := range msg.Key().Text {
			if r >= '0' && r <= '9' {
				m.configuration.input = append(m.configuration.input, r)
			}
		}
	}
	return m, nil
}

func (m *model) handleFrameConfigurationKey(key string) (tea.Model, tea.Cmd) {
	key = navigationKey(key)
	switch key {
	case "up":
		m.configuration.index = (m.configuration.index + 3) % 4
	case "down":
		m.configuration.index = (m.configuration.index + 1) % 4
	case "left":
		m.changeFrameValue(-1)
	case "right":
		m.changeFrameValue(1)
	case "enter":
		if m.configuration.index == 3 {
			return m, m.applyConfiguration()
		}
		m.changeFrameValue(1)
	}
	return m, nil
}

func (m *model) changeFrameValue(delta int) {
	switch m.configuration.index {
	case 0:
		values := []int{5, 6, 7, 8}
		m.configuration.draft.DataBits = cycleInt(values, m.configuration.draft.DataBits, delta)
	case 1:
		values := []string{"none", "odd", "even", "mark", "space"}
		m.configuration.draft.Parity = cycleString(values, m.configuration.draft.Parity, delta)
	case 2:
		values := []string{"1", "1.5", "2"}
		m.configuration.draft.StopBits = cycleString(values, m.configuration.draft.StopBits, delta)
	}
}

func cycleInt(values []int, current, delta int) int {
	index := 0
	for i, value := range values {
		if value == current {
			index = i
			break
		}
	}
	return values[(index+delta+len(values))%len(values)]
}

func cycleString(values []string, current string, delta int) string {
	index := 0
	for i, value := range values {
		if value == current {
			index = i
			break
		}
	}
	return values[(index+delta+len(values))%len(values)]
}

func (m *model) handleConfigurationClick(msg configurationClickMsg) tea.Cmd {
	if msg.mode != m.configuration.mode || m.configuration.applying {
		return nil
	}
	switch msg.mode {
	case configurationPort:
		if msg.index >= 0 && msg.index < len(m.configuration.ports) {
			m.configuration.index = msg.index
		}
	case configurationFrame:
		if msg.index >= 0 && msg.index < 4 {
			m.configuration.index = msg.index
			if msg.index == 3 {
				return m.applyConfiguration()
			}
		}
	}
	return nil
}

func (m *model) applyConfiguration() tea.Cmd {
	configurable := m.endpoint.(configurableEndpoint)
	cfg := m.configuration.draft
	m.configuration.applying = true
	m.configuration.err = nil
	m.status = "Applying serial configuration…"
	return func() tea.Msg {
		return configurationAppliedMsg{config: cfg, err: configurable.Configure(context.Background(), cfg)}
	}
}

func (m *model) handleConfigurationApplied(msg configurationAppliedMsg) {
	if m.configuration.mode == configurationNone {
		return
	}
	m.configuration.applying = false
	if msg.err != nil {
		m.configuration.err = msg.err
		m.status = fmt.Sprintf("Configuration failed: %v", msg.err)
		return
	}
	m.portName = msg.config.PortName
	m.baud = msg.config.BaudRate
	m.dataBits = msg.config.DataBits
	m.parity = msg.config.Parity
	m.stopBits = msg.config.StopBits
	m.connected = true
	returnToConfiguration := m.configuration.returnToConfiguration
	m.configuration = configurationState{}
	m.status = "Serial configuration applied"
	if returnToConfiguration {
		m.focus = focusConfiguration
	}
}

func (m *model) renderConfigurationPopup(width int) string {
	switch m.configuration.mode {
	case configurationPort:
		return m.renderPortPopup(width)
	case configurationBaud:
		return m.renderBaudPopup(width)
	case configurationFrame:
		return m.renderFramePopup(width)
	default:
		return ""
	}
}

func (m *model) renderPortPopup(width int) string {
	rows := []string{sectionTitleStyle.Render("Select serial port"), ""}
	start, end := m.portWindow()
	if m.configuration.loading && len(m.configuration.ports) == 0 {
		rows = append(rows, metaStyle.Render("Refreshing serial ports…"))
	} else if len(m.configuration.ports) == 0 {
		rows = append(rows, metaStyle.Render("No serial ports found"))
	} else {
		for i := start; i < end; i++ {
			port := m.configuration.ports[i]
			prefix := "  "
			if i == m.configuration.index {
				prefix = "› "
			}
			row := fitLine(prefix+port.Name, width-4)
			if i == m.configuration.index {
				row = paletteSelectionStyle.Render(row)
			}
			rows = append(rows, row)
		}
		selected := m.configuration.ports[m.configuration.index]
		rows = append(rows, "", divider(width-4), fieldLabelStyle.Render("DETAILS"))
		details := portDetailLines(selected.Detail, width-4)
		if len(details) == 0 {
			rows = append(rows, metaStyle.Render("No additional device information"))
		} else {
			for _, detail := range details {
				rows = append(rows, metaStyle.Render(detail))
			}
		}
	}
	if m.configuration.err != nil {
		rows = append(rows, "", errorStyle.Render(m.configuration.err.Error()))
	}
	rows = append(rows, "", footerStyle.Render("Live refresh  •  ↑/↓ or j/k select  •  Enter apply  •  Esc/q cancel"))
	return paletteStyle.Width(width).Render(strings.Join(rows, "\n"))
}

func portDetailLines(detail string, width int) []string {
	detail = strings.TrimSpace(detail)
	if detail == "" || width < 1 {
		return nil
	}
	lines := strings.Split(ansi.Hardwrap(detail, width, false), "\n")
	if len(lines) <= 2 {
		return lines
	}
	lines = lines[:2]
	lines[1] = ansi.Truncate(strings.TrimSpace(lines[1]), max(1, width-1), "") + "…"
	return lines
}

func (m *model) portWindow() (start, end int) {
	end = min(len(m.configuration.ports), maxVisiblePorts)
	if m.configuration.index >= end {
		start = m.configuration.index - maxVisiblePorts + 1
		end = min(len(m.configuration.ports), start+maxVisiblePorts)
	}
	return start, end
}

func (m *model) renderBaudPopup(width int) string {
	input := promptStyle.Render("Baud rate › ") + string(m.configuration.input)
	if !m.configuration.applying {
		input += valueStyle.Render("▏")
	}
	rows := []string{sectionTitleStyle.Render("Set baud rate"), "", fitLine(inputBarStyle.Render(input), width-4)}
	if m.configuration.err != nil {
		rows = append(rows, "", errorStyle.Render(m.configuration.err.Error()))
	}
	rows = append(rows, "", footerStyle.Render("Enter apply  •  Esc/q cancel"))
	return paletteStyle.Width(width).Render(strings.Join(rows, "\n"))
}

func (m *model) renderFramePopup(width int) string {
	draft := m.configuration.draft
	labels := []string{
		fmt.Sprintf("Data bits     ‹ %d ›", draft.DataBits),
		fmt.Sprintf("Parity        ‹ %s ›", strings.ToUpper(draft.Parity)),
		fmt.Sprintf("Stop bits     ‹ %s ›", draft.StopBits),
		"Apply",
	}
	rows := []string{sectionTitleStyle.Render("Serial frame"), ""}
	for i, label := range labels {
		prefix := "  "
		if i == m.configuration.index {
			prefix = "› "
			label = paletteSelectionStyle.Render(prefix + label)
		} else {
			label = prefix + label
		}
		rows = append(rows, label)
		if i == 2 {
			rows = append(rows, "")
		}
	}
	if m.configuration.err != nil {
		rows = append(rows, "", errorStyle.Render(m.configuration.err.Error()))
	}
	rows = append(rows, "", footerStyle.Render("↑/↓ or j/k field  •  ←/→ or h/l change  •  Enter  •  Esc/q cancel"))
	return paletteStyle.Width(width).Render(strings.Join(rows, "\n"))
}

func configurationMouseHandler(mode configurationMode, popupX, popupY, popupWidth, portStart, portCount int) func(tea.MouseMsg) tea.Cmd {
	return func(msg tea.MouseMsg) tea.Cmd {
		mouse := msg.Mouse()
		if _, ok := msg.(tea.MouseClickMsg); !ok || mouse.Button != tea.MouseLeft || mouse.X < popupX || mouse.X >= popupX+popupWidth {
			return nil
		}
		contentY := mouse.Y - popupY
		var index = -1
		switch mode {
		case configurationPort:
			visibleIndex := contentY - 4
			if visibleIndex >= 0 && visibleIndex < min(portCount-portStart, maxVisiblePorts) {
				index = portStart + visibleIndex
			} else {
				index = -1
			}
		case configurationFrame:
			switch contentY {
			case 4, 5, 6:
				index = contentY - 4
			case 8:
				index = 3
			}
		}
		if index < 0 {
			return nil
		}
		return func() tea.Msg { return configurationClickMsg{mode: mode, index: index} }
	}
}
