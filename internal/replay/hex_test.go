package replay

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/charmbracelet/x/ansi"
)

func TestHEXFramesPreserveFileOrderAcrossReadBoundariesAndDirectionChanges(t *testing.T) {
	for _, tc := range []struct {
		name, rule string
		records    []capture.Record
		want       []string
	}{
		{"shell_lines", "newline", []capture.Record{
			{Kind: "rx", Data: []byte("a\r")}, {Kind: "rx", Data: []byte("\nB")},
			{Kind: "tx", Data: []byte("cmd\r\n")}, {Kind: "rx", Data: []byte("c\nD\r")},
		}, []string{"rx:61 0D 0A", "rx:42", "tx:63 6D 64 0D 0A", "rx:63 0A", "rx:44 0D"}},
		{"cobs_delimiter", "delimiter:00", []capture.Record{
			{Kind: "rx", Data: []byte{1, 2}}, {Kind: "rx", Data: []byte{0, 3, 4, 0, 5}},
		}, []string{"rx:01 02 00", "rx:03 04 00", "rx:05"}},
		{"split_delimiter", "delimiter:0D0A", []capture.Record{
			{Kind: "rx", Data: []byte{1, 13}}, {Kind: "rx", Data: []byte{10, 2, 13, 10, 3}},
		}, []string{"rx:01 0D 0A", "rx:02 0D 0A", "rx:03"}},
		{"fixed_and_disconnect", "fixed:3", []capture.Record{
			{Kind: "rx", Data: []byte{1}}, {Kind: "tx", Data: []byte{8}},
			{Kind: "rx", Data: []byte{2, 3, 4, 5}}, {Kind: "tx", Data: []byte{9, 10}},
			{Kind: "disconnected"}, {Kind: "rx", Data: []byte{6, 7}},
		}, []string{"rx:01", "tx:08", "rx:02 03", "rx:04 05", "tx:09 0A", "rx:06 07"}},
		{"modbus_request_and_response", "modbus-read", []capture.Record{
			{Kind: "tx", Data: []byte{1, 3, 0, 0, 0, 2, 0xC4, 0x0B}},
			{Kind: "rx", Data: []byte{1, 3}}, {Kind: "rx", Data: []byte{4, 0, 100, 0, 101, 0x7B, 0xC7}},
		}, []string{"tx:01 03 00 00 00 02 C4 0B", "rx:01 03 04 00 64 00 65 7B C7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			framing, err := ParseFrameConfig(tc.rule)
			if err != nil {
				t.Fatal(err)
			}
			h := newHexScreen(Config{Framing: framing})
			var originalTX, originalRX, shownTX, shownRX []byte
			for _, record := range sessionWith(tc.records...).Records {
				if record.Kind == "tx" {
					originalTX = append(originalTX, record.Data...)
				} else if record.Kind == "rx" {
					originalRX = append(originalRX, record.Data...)
				}
				if err := h.record(record); err != nil {
					t.Fatal(err)
				}
			}
			var got []string
			for _, entry := range h.entries {
				got = append(got, entry.direction+":"+hexdata.Format(entry.data))
				if entry.direction == "tx" {
					shownTX = append(shownTX, entry.data...)
				} else {
					shownRX = append(shownRX, entry.data...)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("displayed frame segments:\n got %v\nwant %v", got, tc.want)
			}
			if !bytes.Equal(shownTX, originalTX) || !bytes.Equal(shownRX, originalRX) {
				t.Fatalf("framing lost or duplicated bytes: TX % X RX % X", shownTX, shownRX)
			}
		})
	}
}

func TestLongHEXPlaybackBoundsVisibleHistoryAndKeepsByteTotals(t *testing.T) {
	framing, _ := ParseFrameConfig("chunk")
	h := newHexScreen(Config{Framing: framing})
	for range maxEntries + 100 {
		if err := h.record(capture.Record{Kind: "rx", Data: []byte{1}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(h.entries) != maxEntries || h.rxBytes != maxEntries+100 {
		t.Fatalf("entry history=%d RX total=%d", len(h.entries), h.rxBytes)
	}
	framing, _ = ParseFrameConfig("gap:50ms")
	h = newHexScreen(Config{Framing: framing})
	chunk := bytes.Repeat([]byte{0xAA}, 65536)
	for range 24 {
		if err := h.record(capture.Record{Kind: "rx", Data: chunk, At: time.Unix(1, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	if h.bytes != maxBytes || len(h.entries) != 1 || h.rxBytes != uint64(24*len(chunk)) {
		t.Fatalf("long frame retained=%d entries=%d RX total=%d", h.bytes, len(h.entries), h.rxBytes)
	}
}

func TestGapUsesRecordedTimeSeparatelyForTXAndRX(t *testing.T) {
	framing, err := ParseFrameConfig("gap:50ms")
	if err != nil {
		t.Fatal(err)
	}
	h := newHexScreen(Config{Framing: framing})
	start := sessionWith().Header.Started
	for _, record := range []capture.Record{
		{Kind: "rx", At: start, Data: []byte{1}},
		{Kind: "tx", At: start.Add(40 * time.Millisecond), Data: []byte{9}},
		{Kind: "rx", At: start.Add(60 * time.Millisecond), Data: []byte{2}},
		{Kind: "rx", At: start.Add(90 * time.Millisecond), Data: []byte{3}},
		{Kind: "rx", At: start.Add(140 * time.Millisecond), Data: []byte{4}},
		{Kind: "rx", At: start.Add(191 * time.Millisecond), Data: []byte{5}},
	} {
		if err := h.record(record); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, entry := range h.entries {
		got = append(got, entry.direction+":"+hexdata.Format(entry.data))
	}
	want := []string{"rx:01", "tx:09", "rx:02 03 04", "rx:05"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded gaps produced %v, want %v", got, want)
	}
}

func TestHEXPlaybackShowsIncompleteTailAndOnlyPlaybackControls(t *testing.T) {
	framing, _ := ParseFrameConfig("fixed:4")
	session := sessionWith(capture.Record{Kind: "tx", Data: []byte{0xAA, 1}}, capture.Record{Kind: "rx", Data: []byte{0xBB, 2, 3}}, capture.Record{Kind: "closed"})
	var output bytes.Buffer
	if err := Run(context.Background(), Config{Output: &output, Hexdump: true, Framing: framing}, session); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "AA 01") || !strings.Contains(output.String(), "BB 02 03") || !strings.Contains(output.String(), "complete") {
		t.Fatalf("incomplete tails missing: %q", output.String())
	}
	for _, unwanted := range []string{"HEX >", "checksum", "search", "menu", "speed", "disconnected"} {
		if strings.Contains(output.String(), unwanted) {
			t.Fatalf("unrelated UI %q in replay: %q", unwanted, output.String())
		}
	}
}

func TestHEXWrapsToScreenAndNeverExecutesDeviceControls(t *testing.T) {
	for _, width := range []int{100, 50, 25, 10, 1} {
		var output bytes.Buffer
		h := newHexScreen(Config{Output: &output, Terminal: &testTerminal{}, Size: func() (int, int) { return width, 10 }})
		if err := h.record(capture.Record{Kind: "tx", At: time.Now(), Data: []byte{0xAA}}); err != nil {
			t.Fatal(err)
		}
		if err := h.record(capture.Record{Kind: "rx", At: time.Now(), Data: []byte("\x1b[2J\r\n\xff")}); err != nil {
			t.Fatal(err)
		}
		if err := h.render("paused"); err != nil {
			t.Fatal(err)
		}
		if strings.Count(output.String(), "\x1b[2J") != 1 {
			t.Fatal("recorded device clear-screen escaped the HEX renderer")
		}
		for _, line := range strings.Split(ansi.Strip(output.String()), "\r\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d overflow: %q", width, line)
			}
		}
	}
}
