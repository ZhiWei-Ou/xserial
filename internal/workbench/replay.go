package workbench

import (
	"bytes"
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ZhiWei-Ou/xserial/internal/capture"
)

type replayTickMsg struct{ generation uint64 }

func RunReplay(ctx context.Context, cfg Config, session capture.Session) error {
	cfg.Replay = &session
	return New(cfg).Run(ctx, nil)
}

// Each timer is owned by the model and canceled when playback is paused, its
// speed changes, or the UI exits. Generations reject an already-delivered tick.
func (m *model) scheduleReplay() tea.Cmd {
	if m.replayCancel != nil {
		m.replayCancel()
	}
	m.replayGeneration++
	if m.paused || m.replayIndex >= len(m.cfg.Replay.Records) {
		return nil
	}
	record := m.cfg.Replay.Records[m.replayIndex]
	previous := m.cfg.Replay.Header.Started
	if m.replayIndex > 0 {
		previous = m.cfg.Replay.Records[m.replayIndex-1].At
	}
	delay := record.At.Sub(previous)
	if delay < 0 {
		delay = 0
	}
	delay = time.Duration(float64(delay) / m.speed)
	ctx, cancel := context.WithCancel(m.ctx)
	m.replayCancel = cancel
	generation := m.replayGeneration
	return m.tasks.wrap(func() tea.Msg {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
		return replayTickMsg{generation: generation}
	})
}

func (m *model) advanceReplay() tea.Cmd {
	if m.replayIndex >= len(m.cfg.Replay.Records) {
		return nil
	}
	record := m.cfg.Replay.Records[m.replayIndex]
	m.replayIndex++
	switch record.Kind {
	case "rx":
		m.rxBytes += uint64(len(record.Data))
		frames, err := m.framer.Push(record.Data)
		for _, data := range frames {
			m.appendTraffic(trafficEntry{Direction: "RX", At: record.At, Data: data, Frame: m.cfg.Framing.Kind != "" && m.cfg.Framing.Kind != "chunk"})
		}
		if err != nil {
			m.status = "Framing failed: " + err.Error()
		}
	case "tx":
		m.txBytes += uint64(len(record.Data))
		m.appendTraffic(trafficEntry{Direction: "TX", At: record.At, Data: record.Data})
	case "disconnected", "closed":
		m.connected = false
		m.framer.Reset()
		m.status = record.Kind + ": " + record.Error
	case "connected", "reconnected":
		m.connected = true
		m.status = record.Kind
	case "tx_failed":
		m.status = "Recorded send failed: " + record.Error
	case "mark":
		m.status = "Mark: " + record.Note
		closest := -1
		var distance time.Duration
		for i := range m.entries {
			if m.entries[i].At.Equal(record.At) {
				closest = i
				break
			}
			// Live presentation and wire observations have separate timestamps.
			// Match a selected block/frame by bytes and closest observation time.
			if len(record.Data) > 0 && bytes.Equal(m.entries[i].Data, record.Data) {
				delta := m.entries[i].At.Sub(record.At)
				if delta < 0 {
					delta = -delta
				}
				if closest < 0 || delta < distance {
					closest, distance = i, delta
				}
			}
		}
		if closest >= 0 {
			m.entries[closest].Note = record.Note
			for _, mark := range m.cfg.Marks {
				if mark.At.Equal(m.entries[closest].At) {
					m.entries[closest].Note = mark.Note
				}
			}
		}
	}
	if m.replayIndex == len(m.cfg.Replay.Records) {
		m.paused = true
		m.status = "Replay complete; inspect or search traffic, R restarts"
		return nil
	}
	return m.scheduleReplay()
}

func (m *model) replayKey(key string) (bool, tea.Cmd) {
	switch key {
	case "space":
		m.paused = !m.paused
		m.status = fmt.Sprintf("Playback paused=%v · %.2gx", m.paused, m.speed)
		return true, m.scheduleReplay()
	case "+", "=":
		m.speed = min(16, m.speed*2)
		return true, m.scheduleReplay()
	case "-":
		m.speed = max(0.25, m.speed/2)
		return true, m.scheduleReplay()
	case "r", "R":
		m.replayIndex = 0
		m.entries = nil
		m.matches = nil
		m.selected = 0
		m.trafficBytes = 0
		m.rxBytes = 0
		m.txBytes = 0
		m.scroll = 0
		m.framer.Reset()
		m.paused = false
		return true, m.scheduleReplay()
	}
	return false, nil
}
