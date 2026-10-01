package workbench

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ZhiWei-Ou/xserial/internal/capture"
)

func TestOfflineReplayPauseSpeedCompletionAndNoDeviceWrites(t *testing.T) {
	at := time.Now()
	session := capture.Session{Header: capture.Header{Started: at}, Records: []capture.Record{{Seq: 1, At: at, Kind: "tx", Data: []byte{1, 2}}, {Seq: 2, At: at, Kind: "rx", Data: []byte{3}}, {Seq: 3, At: at, Kind: "closed"}}}
	m := newModel(context.Background(), nil, Config{Replay: &session})
	cmd := m.Init()
	_, next := m.Update(cmd())
	if m.txBytes != 2 || next == nil {
		t.Fatal("recorded TX missing")
	}
	key(m, tea.KeySpace, " ")
	if !m.paused {
		t.Fatal("Space did not pause")
	}
	m.Update(next())
	if m.rxBytes != 0 {
		t.Fatal("stale timer advanced a paused replay")
	}
	cmd = key(m, tea.KeySpace, " ")
	_, next = m.Update(cmd())
	if m.rxBytes != 1 {
		t.Fatal("RX missing after resume")
	}
	m.Update(next())
	if !m.paused || m.replayIndex != 3 || m.connected {
		t.Fatal("replay did not finish for offline inspection")
	}
	m.setInput("AA")
	if key(m, tea.KeyEnter, "") != nil {
		t.Fatal("offline playback tried to send")
	}
	cmd = key(m, 'r', "r")
	if cmd == nil || m.replayIndex != 0 || m.txBytes != 0 {
		t.Fatal("restart did not reset playback")
	}
	m.replayCancel()
	key(m, '+', "+")
	if m.speed != 2 {
		t.Fatal("playback speed did not change")
	}
	m.replayCancel()
}

func TestReplayMarkCanBeSavedAndSurvivesRestart(t *testing.T) {
	at := time.Now()
	session := capture.Session{Header: capture.Header{Started: at}, Records: []capture.Record{
		{Seq: 1, At: at, Kind: "rx", Data: []byte{0xAA}},
		{Seq: 2, At: at.Add(time.Millisecond), Kind: "mark", Data: []byte{0xAA}, Note: "live mark"},
	}}
	path := filepath.Join(t.TempDir(), "capture.xsr.marks.json")
	m := newModel(context.Background(), nil, Config{Replay: &session, MarksPath: path})
	_, next := m.Update(m.Init()())
	m.Update(next())
	if m.entries[0].Note != "live mark" {
		t.Fatal("live mark did not match the wire observation")
	}
	m.action("inspect")
	m.action("mark")
	key(m, 'n', "offline note")
	cmd := key(m, tea.KeyEnter, "")
	m.Update(cmd())
	marks, err := capture.LoadMarks(path)
	if err != nil || len(marks) != 1 || marks[0].Note != "offline note" || m.entries[0].Note != "offline note" {
		t.Fatalf("saved mark: %v, %v", marks, err)
	}
	cmd = key(m, 'r', "r")
	_, next = m.Update(cmd())
	m.Update(next())
	if m.entries[0].Note != "offline note" {
		t.Fatal("restart lost the saved note")
	}
}
