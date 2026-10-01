package workbench

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
)

func TestAssembleChecksumInspectRegisterAndCopySelection(t *testing.T) {
	m, endpoint := testModel()
	m.setInput("01 03 00 00 00 02")
	m.action("checksum")
	if !strings.Contains(strings.Join(m.checksumLines(), "\n"), "01 03 00 00 00 02 C4 0B") {
		t.Fatal("final packet not previewed")
	}
	key(m, tea.KeyEnter, "")
	if string(m.input) != "01 03 00 00 00 02 C4 0B" || len(endpoint.sent) != 0 {
		t.Fatal("checksum action sent without review")
	}
	cmd := key(m, tea.KeyEnter, "")
	m.Update(cmd())
	response, _ := hexdata.AppendChecksum([]byte{1, 3, 4, 0, 100, 0, 101}, "crc16-modbus")
	m.handleEvent(middleware.Received{Data: response, At: time.Now()})
	key(m, tea.KeyTab, "")
	if m.modal != "inspect" {
		t.Fatal("Tab did not open byte inspector")
	}
	for range 3 {
		key(m, tea.KeyRight, "")
	}
	lines := strings.Join(m.inspectionLines(), "\n")
	if !strings.Contains(lines, "100") || !strings.Contains(lines, "crc16-modbus: OK") {
		t.Fatalf("inspection = %q", lines)
	}
	key(m, tea.KeyEnter, "")
	if string(m.input) != "00 64" || m.modal != "" {
		t.Fatal("selection was not copied to editor")
	}
}

func TestFramingSurvivesSplitReadsAndResetsOnDisconnect(t *testing.T) {
	m, _ := testModel()
	m.cfg.Framing, _ = hexdata.ParseFrameConfig("modbus-read")
	m.framer = hexdata.NewFramer(m.cfg.Framing)
	response, _ := hexdata.AppendChecksum([]byte{1, 3, 4, 0, 100, 0, 101}, "crc16-modbus")
	m.handleEvent(middleware.Received{Data: response[:3], At: time.Now()})
	if len(m.entries) != 0 || m.framer.Pending() != 3 || m.rxBytes != 3 {
		t.Fatal("partial frame mislabeled as complete")
	}
	m.handleEvent(middleware.Received{Data: response[3:], At: time.Now()})
	if len(m.entries) != 1 || !m.entries[0].Frame || m.rxBytes != 9 {
		t.Fatal("complete frame missing")
	}
	m.handleEvent(middleware.Received{Data: response[:3]})
	m.handleEvent(middleware.Disconnected{Err: middleware.ErrDisconnected})
	if m.framer.Pending() != 0 || len(m.entries) != 1 {
		t.Fatal("partial data crossed a disconnect")
	}
}

func TestSearchFindsBytesAndCanNavigateMatches(t *testing.T) {
	m, _ := testModel()
	for _, data := range [][]byte{{0xAA, 1}, {0xBB}, {0xBB, 0xAA, 2}} {
		m.appendTraffic(trafficEntry{Direction: "RX", At: time.Now(), Data: data})
	}
	m.action("search")
	key(m, 'a', "AA")
	key(m, tea.KeyEnter, "")
	if m.modal != "inspect" || len(m.matches) != 2 || m.selected != 0 {
		t.Fatal("search did not select first match")
	}
	key(m, 'n', "n")
	if m.selected != 2 || m.selectionOffset != 1 {
		t.Fatal("search did not select next match")
	}
	m.modal = ""
	m.action("clear")
	m.appendTraffic(trafficEntry{Direction: "RX", At: time.Now(), Data: []byte{0xAA}})
	m.action("inspect")
	key(m, 'n', "n")
	if m.selected != 0 || len(m.matches) != 0 {
		t.Fatal("cleared history retained stale search results")
	}
}

func TestMultilineHexPasteCannotAlterTerminalLayout(t *testing.T) {
	m, endpoint := testModel()
	m.Update(tea.PasteMsg{Content: "AA\r\n01\t02"})
	if string(m.input) != "AA  01 02" {
		t.Fatalf("paste = %q", m.input)
	}
	cmd := key(m, tea.KeyEnter, "")
	m.Update(cmd())
	if len(endpoint.sent[0]) != 3 {
		t.Fatal("multiline hex not parsed")
	}
	m.setInput("")
	m.Update(tea.PasteMsg{Content: "AA\x1b[2J"})
	if key(m, tea.KeyEnter, "") != nil {
		t.Fatal("control characters were sent as valid hex")
	}
}

func TestSearchPasteIsBoundedAndCannotAlterLayout(t *testing.T) {
	m, _ := testModel()
	m.action("search")
	m.Update(tea.PasteMsg{Content: "AA\r\n01"})
	if string(m.query) != "AA  01" {
		t.Fatalf("search paste = %q", m.query)
	}
	m.Update(tea.PasteMsg{Content: strings.Repeat("A", maxInputChars)})
	if len(m.query) != 6 {
		t.Fatal("search paste exceeded its limit")
	}
}
