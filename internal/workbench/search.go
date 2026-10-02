package workbench

import (
	"bytes"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
)

func (m *model) insertQuery(text string) {
	runes := editorRunes(text)
	if len(m.query)+len(runes) > maxInputChars {
		m.status = "Search limit is 16384 characters"
		return
	}
	m.query = append(m.query, runes...)
}

func (m *model) handleSearchKey(msg tea.KeyPressMsg) {
	switch msg.Keystroke() {
	case "backspace":
		if len(m.query) > 0 {
			m.query = m.query[:len(m.query)-1]
		}
	case "enter":
		pattern, err := hexdata.Parse(string(m.query))
		if err != nil || len(pattern) == 0 {
			m.status = "Search requires valid hex bytes"
			return
		}
		m.matches = nil
		for i, entry := range m.entries {
			if bytes.Contains(entry.Data, pattern) {
				m.matches = append(m.matches, i)
			}
		}
		if len(m.matches) == 0 {
			m.status = "No matching data block or frame"
			return
		}
		m.matchIndex = 0
		m.selected = m.matches[0]
		m.selectionOffset = bytes.Index(m.entries[m.selected].Data, pattern)
		m.selectionWidth = len(pattern)
		m.modal = "inspect"
		m.status = fmt.Sprintf("%d matching entries; N selects next match", len(m.matches))
	default:
		m.insertQuery(msg.Text)
	}
}

func (m *model) handleMarkKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.Keystroke() {
	case "backspace":
		if len(m.name) > 0 {
			m.name = m.name[:len(m.name)-1]
		}
	case "enter":
		note := strings.TrimSpace(string(m.name))
		if note == "" {
			m.status = "A mark note is required"
			return nil
		}
		entry := m.entries[m.selected]
		id := entry.ID
		recorder := m.cfg.Recorder
		m.modal = ""
		m.marking = true
		return m.tasks.wrap(func() tea.Msg {
			_, err := recorder.Append(capture.Record{Kind: "mark", At: entry.At, Data: entry.Data, Note: note})
			return markedMsg{id: id, at: entry.At, note: note, err: err}
		})
	default:
		m.insertName(msg.Text)
	}
	return nil
}
