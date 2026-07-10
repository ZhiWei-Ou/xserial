package tui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

type fakePort struct {
	bytes.Buffer
}

func (p *fakePort) Close() error {
	return nil
}

func TestTUIShowsAndRecordsReceivedData(t *testing.T) {
	port := &fakePort{}
	var log bytes.Buffer
	m := newModel(context.Background(), Config{
		Port:       port,
		ReceiveLog: &log,
		TimeFormat: "15:04:05",
	})

	at := time.Date(2026, time.July, 10, 12, 34, 56, 0, time.Local)
	m.Update(serialDataMsg{data: []byte("first\r\nsecond"), at: at})

	if len(m.lines) != 1 || m.lines[0] != "[12:34:56] first" || m.currentLine != "[12:34:56] second" {
		t.Fatalf("transcript lines=%q current=%q", m.lines, m.currentLine)
	}
	if got := log.String(); got != "[12:34:56] first\r\n[12:34:56] second" {
		t.Fatalf("receive log = %q", got)
	}
	if m.rxBytes != int64(len("first\r\nsecond")) {
		t.Fatalf("RX bytes = %d", m.rxBytes)
	}
}

func TestTUISendsEnteredLine(t *testing.T) {
	port := &fakePort{}
	m := newModel(context.Background(), Config{Port: port})

	for _, text := range []string{"v", "e", "r"} {
		m.Update(tea.KeyPressMsg(tea.Key{Code: []rune(text)[0], Text: text}))
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))

	if got := port.String(); got != "ver\r" {
		t.Fatalf("serial output = %q, want ver\\r", got)
	}
	if m.txBytes != 4 {
		t.Fatalf("TX bytes = %d, want 4", m.txBytes)
	}
}

func TestTUIMarksUserRequestedQuit(t *testing.T) {
	m := newModel(context.Background(), Config{Port: &fakePort{}})

	_, command := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))

	if !m.quitRequested {
		t.Fatal("quitRequested = false, want true")
	}
	if command == nil {
		t.Fatal("quit command = nil")
	}
}

func TestTUIDefaultRecordingKeepsRawBytes(t *testing.T) {
	port := &fakePort{}
	var log bytes.Buffer
	m := newModel(context.Background(), Config{Port: port, ReceiveLog: &log})
	want := []byte{'a', 0, 'b', '\n'}

	m.Update(serialDataMsg{data: want, at: time.Now()})

	if got := log.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("receive log = %v, want %v", got, want)
	}
}

func TestTUIViewUsesAlternateScreen(t *testing.T) {
	m := newModel(context.Background(), Config{
		Port:     &fakePort{},
		PortName: "/dev/ttyUSB0",
		Baud:     115200,
		Frame:    "8,N,1",
	})
	m.width = 100
	m.height = 30

	view := m.View()
	if !view.AltScreen {
		t.Fatal("AltScreen = false, want true")
	}
	for _, text := range []string{"XSERIAL", "CONNECTED", "/dev/ttyUSB0", "Ctrl+U upload"} {
		if !strings.Contains(view.Content, text) {
			t.Fatalf("view does not contain %q", text)
		}
	}
}

var _ io.ReadWriteCloser = (*fakePort)(nil)
