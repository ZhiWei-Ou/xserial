package workbench

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
	"time"
)

type trafficEntry struct {
	Direction string
	At        time.Time
	Data      []byte
	Frame     bool
	Note      string
	ID        uint64
}

type eventMsg struct{ event middleware.Event }
type closedMsg struct{}
type sentMsg struct {
	data  []byte
	input string
	at    time.Time
	err   error
}
type savedMsg struct {
	favorites []Favorite
	err       error
}
type markedMsg struct {
	id   uint64
	at   time.Time
	note string
	err  error
}

type model struct {
	ctx              context.Context
	endpoint         middleware.Endpoint
	events           <-chan middleware.Event
	cfg              Config
	width, height    int
	input            []rune
	cursor           int
	entries          []trafficEntry
	trafficBytes     int
	rxBytes, txBytes uint64
	connected        bool
	sending          bool
	status           string
	history          []string
	historyIndex     int
	draft            string
	scroll           int // Rows above the tail; zero follows incoming traffic.
	palette          bool
	paletteIndex     int
	favorites        []Favorite
	favoriteIndex    int
	modal            string
	name             []rune
	framer           *hexdata.Framer
	selected         int
	selectionOffset  int
	selectionWidth   int
	checksumIndex    int
	saving           bool
	query            []rune
	matches          []int
	matchIndex       int
	marking          bool
	nextID           uint64
	tasks            commandTasks
}

func newModel(ctx context.Context, endpoint middleware.Endpoint, cfg Config) *model {
	m := &model{ctx: ctx, endpoint: endpoint, cfg: cfg, width: 100, height: 28,
		connected: true, status: "Ready", favorites: append([]Favorite(nil), cfg.Favorites...)}
	m.framer = hexdata.NewFramer(cfg.Framing)
	m.selectionWidth = 2
	if endpoint != nil {
		m.events = endpoint.Events()
	}
	if cfg.Demo {
		m.setInput("01 03 00 00 00 02 C4 0B")
		m.status = "Demo: Enter queries two registers; 02 selects a bad CRC; 03 splits the response"
	}
	return m
}

func (m *model) Init() tea.Cmd {
	return m.waitEvent()
}

func (m *model) waitEvent() tea.Cmd {
	return m.tasks.wrap(func() tea.Msg {
		select {
		case event, ok := <-m.events:
			if !ok {
				return closedMsg{}
			}
			return eventMsg{event}
		case <-m.ctx.Done():
			return closedMsg{}
		}
	})
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
		m.clampScroll()
	case closedMsg:
		return m, tea.Quit
	case eventMsg:
		m.handleEvent(msg.event)
		return m, m.waitEvent()
	case sentMsg:
		m.sending = false
		if msg.err != nil {
			m.status = "Send failed: " + msg.err.Error()
			return m, nil
		}
		m.txBytes += uint64(len(msg.data))
		m.appendTraffic(trafficEntry{Direction: "TX", At: msg.at, Data: msg.data})
		m.remember(msg.input)
		m.status = fmt.Sprintf("Sent %d bytes", len(msg.data))
	case savedMsg:
		m.saving = false
		if msg.err != nil {
			m.status = "Save failed: " + msg.err.Error()
		} else {
			m.favorites = msg.favorites
			m.status = "Command saved"
		}
	case markedMsg:
		m.marking = false
		if msg.err != nil {
			m.status = "Mark failed: " + msg.err.Error()
		} else {
			for i := range m.entries {
				if m.entries[i].ID == msg.id {
					m.entries[i].Note = msg.note
					break
				}
			}
			m.status = "Mark saved: " + msg.note
		}
	case tea.PasteMsg:
		if m.modal == "name" || m.modal == "mark" {
			m.insertName(msg.Content)
		} else if m.modal == "search" {
			m.insertQuery(msg.Content)
		} else if m.modal == "" && !m.palette {
			m.insert(msg.Content)
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleEvent(event middleware.Event) {
	switch event := event.(type) {
	case middleware.Received:
		m.rxBytes += uint64(len(event.Data))
		frames, err := m.framer.Push(event.Data)
		for _, data := range frames {
			m.appendTraffic(trafficEntry{Direction: "RX", At: event.At, Data: data, Frame: m.cfg.Framing.Kind != "" && m.cfg.Framing.Kind != "chunk"})
		}
		if err != nil {
			m.status = "Framing failed: " + err.Error()
		}
	case middleware.Disconnected:
		m.connected = false
		m.framer.Reset()
		m.status = "Disconnected: " + event.Err.Error()
	case middleware.Reconnecting:
		m.connected = false
		m.status = fmt.Sprintf("Reconnecting, attempt %d", event.Attempt)
	case middleware.Reconnected:
		m.connected = true
		m.status = "Reconnected"
	}
}

func (m *model) appendTraffic(entry trafficEntry) {
	before := m.trafficRows()
	entry.Data = append([]byte(nil), entry.Data...)
	m.nextID++
	entry.ID = m.nextID
	m.entries = append(m.entries, entry)
	m.trafficBytes += len(entry.Data)
	removed := 0
	for len(m.entries) > maxEntries || m.trafficBytes > maxTrafficBytes {
		m.trafficBytes -= len(m.entries[0].Data)
		removed += m.entryRows(m.entries[0])
		m.entries = m.entries[1:]
		m.selected = max(0, m.selected-1)
		m.matches = nil
	}
	if m.scroll > 0 {
		m.scroll += m.trafficRows() - before + removed
	}
	m.clampScroll()
}
