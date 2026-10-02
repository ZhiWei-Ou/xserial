package workbench

import (
	tea "charm.land/bubbletea/v2"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"strings"
	"time"
	"unicode"
)

func editorRunes(input string) []rune {
	runes := []rune(input)
	for i, r := range runes {
		if unicode.IsSpace(r) {
			runes[i] = ' '
		} else if unicode.IsControl(r) {
			runes[i] = '�'
		}
	}
	return runes
}

func (m *model) setInput(input string) { m.input = editorRunes(input); m.cursor = len(m.input) }

func (m *model) insert(text string) {
	if m.sending {
		return
	}
	runes := editorRunes(text)
	if len(m.input)+len(runes) > maxInputChars {
		m.status = "Input limit is 16384 characters"
		return
	}
	next := append([]rune(nil), m.input[:m.cursor]...)
	next = append(next, runes...)
	next = append(next, m.input[m.cursor:]...)
	m.input = next
	m.cursor += len(runes)
}

func (m *model) remember(input string) {
	if len(m.history) == 0 || m.history[len(m.history)-1] != input {
		m.history = append(m.history, input)
	}
	if len(m.history) > maxHistory {
		m.history = m.history[len(m.history)-maxHistory:]
	}
	m.historyIndex = len(m.history)
	m.draft = input
}

func (m *model) browseHistory(delta int) {
	if m.sending || len(m.history) == 0 {
		return
	}
	if m.historyIndex == len(m.history) {
		m.draft = string(m.input)
	}
	m.historyIndex = min(len(m.history), max(0, m.historyIndex+delta))
	if m.historyIndex == len(m.history) {
		m.setInput(m.draft)
	} else {
		m.setInput(m.history[m.historyIndex])
	}
}

func (m *model) send() tea.Cmd {
	if m.sending {
		return nil
	}
	if !m.connected {
		m.status = "Not connected; nothing was queued"
		return nil
	}
	data, err := hexdata.Parse(string(m.input))
	if err != nil {
		m.status = err.Error()
		return nil
	}
	if len(data) == 0 {
		m.status = "Enter at least one hex byte"
		return nil
	}
	input := hexdata.Format(data)
	m.setInput(input)
	m.sending = true
	m.status = "Sending..."
	return m.tasks.wrap(func() tea.Msg {
		err := m.endpoint.Send(m.ctx, data)
		return sentMsg{data: data, input: input, at: time.Now(), err: err}
	})
}

type localCommand struct {
	label  string
	key    string
	action string
}

var commands = []localCommand{
	{"Save command", "ctrl+s", "save"},
	{"Load command", "ctrl+o", "load"},
	{"Inspect traffic bytes", "ctrl+i", "inspect"},
	{"Append checksum", "ctrl+k", "checksum"},
	{"Search hex bytes", "ctrl+f", "search"},
	{"Mark selected traffic", "ctrl+b", "mark"},
	{"Follow latest traffic", "end", "follow"},
	{"Clear traffic", "ctrl+l", "clear"},
	{"Quit", "ctrl+c", "quit"},
}

func (m *model) action(action string) tea.Cmd {
	switch action {
	case "quit":
		if m.endpoint != nil {
			m.endpoint.Quit()
		}
		return tea.Quit
	case "follow":
		m.scroll = 0
	case "clear":
		m.entries = nil
		m.matches = nil
		m.selected = 0
		m.trafficBytes = 0
		m.scroll = 0
		m.status = "Traffic cleared"
		m.framer.Reset()
	case "save":
		if m.saving {
			m.status = "A save is already in progress"
			return nil
		}
		data, err := hexdata.Parse(string(m.input))
		if err != nil || len(data) == 0 {
			m.status = "Enter a valid command before saving"
			return nil
		}
		if m.cfg.FavoritesPath == "" {
			m.status = "Favorites file is not configured"
			return nil
		}
		m.modal, m.name = "name", nil
	case "load":
		if len(m.favorites) == 0 {
			m.status = "No saved commands; Ctrl-S saves the current input"
		} else {
			m.modal = "favorites"
			m.favoriteIndex = 0
		}
	case "inspect":
		if len(m.entries) == 0 {
			m.status = "No traffic to inspect"
			return nil
		}
		m.selected = len(m.entries) - 1
		m.selectionOffset = 0
		m.modal = "inspect"
	case "checksum":
		if m.sending {
			return nil
		}
		m.modal = "checksum"
		m.checksumIndex = 0
	case "search":
		m.modal = "search"
		m.query = nil
	case "mark":
		if m.marking {
			return nil
		}
		if len(m.entries) == 0 {
			m.status = "No traffic to mark"
			return nil
		}
		if m.cfg.Recorder == nil {
			m.status = "Use --record to save live marks"
			return nil
		}
		if m.modal != "inspect" {
			m.selected = len(m.entries) - 1
		}
		m.modal = "mark"
		m.name = nil
	}
	return nil
}

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Keystroke()
	if key == "tab" {
		key = "ctrl+i"
	}
	if key == "ctrl+c" {
		return m, m.action("quit")
	}
	if key == "ctrl+b" && m.modal == "inspect" {
		return m, m.action("mark")
	}
	if m.modal != "" {
		return m.handleModal(msg)
	}
	if key == "ctrl+p" {
		m.palette = !m.palette
		m.paletteIndex = 0
		return m, nil
	}
	if m.palette {
		switch key {
		case "esc":
			m.palette = false
		case "up", "k":
			m.paletteIndex = (m.paletteIndex + len(commands) - 1) % len(commands)
		case "down", "j":
			m.paletteIndex = (m.paletteIndex + 1) % len(commands)
		case "enter":
			m.palette = false
			return m, m.action(commands[m.paletteIndex].action)
		}
		return m, nil
	}
	for _, command := range commands {
		if key == command.key {
			return m, m.action(command.action)
		}
	}
	switch key {
	case "enter":
		return m, m.send()
	case "up":
		m.browseHistory(-1)
	case "down":
		m.browseHistory(1)
	case "pgup", "shift+pgup":
		m.scroll += m.bodyHeight()
		m.clampScroll()
	case "pgdown", "shift+pgdown":
		m.scroll -= m.bodyHeight()
		m.clampScroll()
	case "left":
		m.cursor = max(0, m.cursor-1)
	case "right":
		m.cursor = min(len(m.input), m.cursor+1)
	case "home":
		m.cursor = 0
	case "backspace":
		if !m.sending && m.cursor > 0 {
			m.input = append(m.input[:m.cursor-1], m.input[m.cursor:]...)
			m.cursor--
		}
	case "delete":
		if !m.sending && m.cursor < len(m.input) {
			m.input = append(m.input[:m.cursor], m.input[m.cursor+1:]...)
		}
	case "ctrl+u":
		if !m.sending {
			m.setInput("")
		}
	default:
		if msg.Text != "" {
			m.insert(msg.Text)
		}
	}
	return m, nil
}

func (m *model) handleModal(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Keystroke()
	if key == "esc" {
		m.modal = ""
		return m, nil
	}
	if m.modal == "inspect" {
		m.handleInspectionKey(key)
		return m, nil
	}
	if m.modal == "checksum" {
		m.handleChecksumKey(key)
		return m, nil
	}
	if m.modal == "search" {
		m.handleSearchKey(msg)
		return m, nil
	}
	if m.modal == "mark" {
		return m, m.handleMarkKey(msg)
	}
	if m.modal == "favorites" {
		switch key {
		case "up", "k":
			m.favoriteIndex = (m.favoriteIndex + len(m.favorites) - 1) % len(m.favorites)
		case "down", "j":
			m.favoriteIndex = (m.favoriteIndex + 1) % len(m.favorites)
		case "enter":
			m.setInput(m.favorites[m.favoriteIndex].Hex)
			m.modal = ""
			m.status = "Loaded " + m.favorites[m.favoriteIndex].Name
		}
		return m, nil
	}
	switch key {
	case "backspace":
		if len(m.name) > 0 {
			m.name = m.name[:len(m.name)-1]
		}
	case "enter":
		name := strings.TrimSpace(string(m.name))
		if name == "" {
			m.status = "A command name is required"
			return m, nil
		}
		data, _ := hexdata.Parse(string(m.input))
		favorites := append([]Favorite(nil), m.favorites...)
		index := -1
		for i, favorite := range favorites {
			if favorite.Name == name {
				index = i
				break
			}
		}
		favorite := Favorite{Name: name, Hex: hexdata.Format(data)}
		if index >= 0 {
			favorites[index] = favorite
		} else {
			favorites = append(favorites, favorite)
		}
		m.modal = ""
		m.saving = true
		path := m.cfg.FavoritesPath
		return m, m.tasks.wrap(func() tea.Msg { return savedMsg{favorites: favorites, err: SaveFavorites(path, favorites)} })
	default:
		m.insertName(msg.Text)
	}
	return m, nil
}
