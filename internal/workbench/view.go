package workbench

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

func (m *model) bodyHeight() int { return max(1, m.height-7) }
func (m *model) bytesPerRow() int {
	if m.width < 50 {
		return min(16, max(1, (m.width-3)/3))
	}
	return min(16, max(1, (m.width-26)/4))
}
func (m *model) entryRows(entry trafficEntry) int {
	return max(1, (len(entry.Data)+m.bytesPerRow()-1)/m.bytesPerRow())
}
func (m *model) trafficRows() int {
	rows := 0
	for _, entry := range m.entries {
		rows += m.entryRows(entry)
	}
	return rows
}
func (m *model) clampScroll() {
	m.scroll = min(max(0, m.scroll), max(0, m.trafficRows()-m.bodyHeight()))
}

func (m *model) trafficLines() []string {
	var lines []string
	rowSize := m.bytesPerRow()
	end := max(0, m.trafficRows()-m.scroll)
	start := max(0, end-m.bodyHeight())
	row := 0
	for _, entry := range m.entries {
		if row+m.entryRows(entry) <= start {
			row += m.entryRows(entry)
			continue
		}
		for offset := 0; offset < len(entry.Data); offset += rowSize {
			if row >= end {
				return lines
			}
			row++
			if row <= start {
				continue
			}
			data := entry.Data[offset:min(len(entry.Data), offset+rowSize)]
			prefix := fmt.Sprintf("%s %s %4d ", entry.At.Format("15:04:05.000"), entry.Direction, len(entry.Data))
			if m.width < 50 {
				prefix = entry.Direction + " "
			}
			if offset > 0 {
				prefix = strings.Repeat(" ", len(prefix))
			}
			line := prefix + fmt.Sprintf("%-*s", rowSize*3-1, hexdata.Format(data))
			if m.width >= 50 {
				line += "  |" + hexdata.ASCII(data) + "|"
			}
			color := lipgloss.Color("#6BCBFF")
			if entry.Direction == "TX" {
				color = lipgloss.Color("#FFB86C")
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(color).Render(line))
		}
	}
	return lines
}

func (m *model) View() tea.View {
	width := m.width
	connection := "connected"
	if !m.connected {
		connection = "disconnected"
	}
	mode := "LIVE"
	if m.cfg.Demo {
		mode = "DEMO"
	}
	if m.cfg.Replay != nil {
		mode = fmt.Sprintf("REPLAY %.2gx", m.speed)
	}
	header := fmt.Sprintf(" xserial %s  %s  %d %d/%s/%s  %s  RX %d  TX %d", mode,
		m.cfg.Connection.PortName, m.cfg.Connection.BaudRate, m.cfg.Connection.DataBits,
		m.cfg.Connection.Parity, m.cfg.Connection.StopBits, connection, m.rxBytes, m.txBytes)
	lines := []string{accent.Render(fit(header, width)), accent.Render(strings.Repeat("─", width))}
	visible := m.trafficLines()
	if m.trafficRows() == 0 {
		visible = []string{" No traffic yet. Enter a hex command below."}
	}
	if m.palette {
		visible = []string{" Commands · Esc closes"}
		first := max(0, m.paletteIndex-m.bodyHeight()+2)
		for i := first; i < min(len(commands), first+m.bodyHeight()-1); i++ {
			command := commands[i]
			marker := "  "
			if i == m.paletteIndex {
				marker = "> "
			}
			visible = append(visible, marker+command.label+"  "+command.key)
		}
	} else if m.modal == "inspect" {
		visible = m.inspectionLines()
	} else if m.modal == "checksum" {
		visible = m.checksumLines()
	} else if m.modal == "favorites" {
		visible = []string{" Saved commands · Enter loads · Esc closes"}
		first := max(0, m.favoriteIndex-m.bodyHeight()+2)
		for i := first; i < min(len(m.favorites), first+m.bodyHeight()-1); i++ {
			marker := "  "
			if i == m.favoriteIndex {
				marker = "> "
			}
			visible = append(visible, marker+m.favorites[i].Name+"  "+m.favorites[i].Hex)
		}
	}
	for i := 0; i < m.bodyHeight(); i++ {
		line := ""
		if i < len(visible) {
			line = visible[i]
		}
		lines = append(lines, fit(line, width))
	}
	lines = append(lines, accent.Render(strings.Repeat("─", width)))
	input := " HEX > " + string(m.input[:m.cursor]) + "│" + string(m.input[m.cursor:])
	if m.modal == "name" {
		input = " Save as > " + string(m.name) + "│"
	}
	if m.modal == "mark" {
		input = " Mark note > " + string(m.name) + "│"
	}
	if m.modal == "search" {
		input = " Search hex > " + string(m.query) + "│"
	}
	lines = append(lines, fit(input, width))
	preview := ""
	if data, err := hexdata.Parse(string(m.input)); err != nil {
		preview = err.Error()
	} else {
		preview = fmt.Sprintf(" %d bytes  %s", len(data), hexdata.Format(data))
		if m.framer.Pending() > 0 {
			preview += fmt.Sprintf("  | RX frame pending: %d bytes", m.framer.Pending())
		}
	}
	help := " Enter send  ↑↓ history  Tab inspect  Ctrl-K checksum  Ctrl-P menu  Ctrl-C quit"
	if m.cfg.Replay != nil {
		help = " Space pause  +/- speed  R restart  Tab inspect  Ctrl-F search  Ctrl-P menu  Ctrl-C quit"
	}
	if width < 60 {
		help = " Enter send  Tab inspect  ^P menu  ^C quit"
		if m.cfg.Replay != nil {
			help = " Space pause  +/- speed  Tab inspect  ^C quit"
		}
	}
	lines = append(lines, fit(preview, width), fit(" "+m.status, width), fit(help, width))
	view := tea.NewView(strings.Join(lines[:min(len(lines), m.height)], "\n"))
	view.AltScreen = true
	return view
}

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#F49B67"))

func fit(text string, width int) string { return ansi.Truncate(text, width, "") }
