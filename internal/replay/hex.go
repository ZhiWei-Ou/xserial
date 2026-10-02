package replay

import (
	"fmt"
	"strings"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/transfer"
	"github.com/charmbracelet/x/ansi"
)

const (
	maxEntries = 2000
	maxBytes   = 1 << 20
)

type hexEntry struct {
	direction string
	at        time.Time
	data      []byte
	ended     bool
}

type hexStream struct {
	decoder *hexdata.Framer
	last    time.Time
}

type hexScreen struct {
	cfg              Config
	tx, rx           hexStream
	entries          []hexEntry
	bytes            int
	txBytes, rxBytes uint64
	renderedWidth    int
	renderedHeight   int
}

func newHexScreen(cfg Config) *hexScreen {
	if cfg.Framing.Bytes.Kind == "" && cfg.Framing.Gap == 0 {
		cfg.Framing, _ = ParseFrameConfig("newline")
	}
	txFraming := cfg.Framing.Bytes
	if txFraming.Kind == "modbus-read" {
		// This decoder describes Modbus responses; requests use another layout.
		txFraming = hexdata.FrameConfig{Kind: "chunk"}
	}
	return &hexScreen{cfg: cfg,
		tx: hexStream{decoder: hexdata.NewFramer(txFraming)},
		rx: hexStream{decoder: hexdata.NewFramer(cfg.Framing.Bytes)}}
}

func (h *hexScreen) record(record capture.Record) error {
	if record.Kind == "disconnected" || record.Kind == "closed" {
		h.endFrame("tx")
		h.endFrame("rx")
		h.tx.decoder.Reset()
		h.rx.decoder.Reset()
		h.tx.last, h.rx.last = time.Time{}, time.Time{}
		return nil
	}
	if (record.Kind != "tx" && record.Kind != "rx") || len(record.Data) == 0 {
		return nil
	}
	stream := &h.rx
	if record.Kind == "tx" {
		stream = &h.tx
		h.txBytes += uint64(len(record.Data))
	} else {
		h.rxBytes += uint64(len(record.Data))
	}
	if h.cfg.Framing.Gap > 0 {
		if !stream.last.IsZero() && record.At.Sub(stream.last) > h.cfg.Framing.Gap {
			h.endFrame(record.Kind)
		}
		h.append(record.Kind, record.At, record.Data, false)
		stream.last = record.At
		return nil
	}

	// A completed frame can begin in earlier records. Map its end back to the
	// current record, displaying each arriving byte immediately and in file order.
	pending := stream.decoder.Pending()
	frames, err := stream.decoder.Push(record.Data)
	position := 0
	for _, frame := range frames {
		n := len(frame) - pending
		pending = 0
		h.append(record.Kind, record.At, record.Data[position:position+n], true)
		position += n
	}
	if position < len(record.Data) {
		h.append(record.Kind, record.At, record.Data[position:], false)
	}
	if err != nil {
		return fmt.Errorf("frame recorded %s traffic: %w", strings.ToUpper(record.Kind), err)
	}
	return nil
}

func (h *hexScreen) endFrame(direction string) {
	if len(h.entries) > 0 && h.entries[len(h.entries)-1].direction == direction {
		h.entries[len(h.entries)-1].ended = true
	}
}

func (h *hexScreen) append(direction string, at time.Time, data []byte, ended bool) {
	if len(data) == 0 {
		return
	}
	if len(h.entries) == 0 || h.entries[len(h.entries)-1].direction != direction || h.entries[len(h.entries)-1].ended {
		h.entries = append(h.entries, hexEntry{direction: direction, at: at})
	}
	entry := &h.entries[len(h.entries)-1]
	entry.data = append(entry.data, data...)
	entry.ended = ended
	h.bytes += len(data)
	for len(h.entries) > maxEntries || (h.bytes > maxBytes && len(h.entries) > 1) {
		h.bytes -= len(h.entries[0].data)
		h.entries[0] = hexEntry{}
		h.entries = h.entries[1:]
	}
	if h.bytes > maxBytes {
		entry = &h.entries[0]
		entry.data = append([]byte(nil), entry.data[len(entry.data)-maxBytes:]...)
		h.bytes = maxBytes
	}
}

func (h *hexScreen) render(state string) error {
	width, height := 100, 28
	if h.cfg.Size != nil {
		width, height = h.cfg.Size()
	}
	width, height = max(1, width), max(1, height)
	rowSize := min(16, max(1, (width-26)/4))
	if width < 50 {
		rowSize = min(16, max(1, (width-3)/3))
	}
	interactive := h.cfg.Terminal != nil
	rows := 0
	for _, entry := range h.entries {
		rows += (len(entry.data) + rowSize - 1) / rowSize
	}
	skip := 0
	if interactive {
		skip = max(0, rows-max(0, height-2))
	}
	lines := []string{ansi.Truncate(fmt.Sprintf("xserial REPLAY HEX · %s · RX %d TX %d", state, h.rxBytes, h.txBytes), width, "")}
	for _, entry := range h.entries {
		for offset := 0; offset < len(entry.data); offset += rowSize {
			if skip > 0 {
				skip--
				continue
			}
			data := entry.data[offset:min(len(entry.data), offset+rowSize)]
			prefix := fmt.Sprintf("%s %s %5d ", entry.at.Format("15:04:05.000"), strings.ToUpper(entry.direction), len(entry.data))
			if width < 50 {
				prefix = strings.ToUpper(entry.direction) + " "
			}
			if offset > 0 {
				prefix = strings.Repeat(" ", len(prefix))
			}
			line := prefix + fmt.Sprintf("%-*s", rowSize*3-1, hexdata.Format(data))
			if width >= 50 {
				line += "  |" + hexdata.ASCII(data) + "|"
			}
			line = ansi.Truncate(line, width, "")
			if interactive {
				color := "\x1b[38;2;107;203;255m"
				if entry.direction == "tx" {
					color = "\x1b[38;2;255;184;108m"
				}
				line = color + line + "\x1b[0m"
			}
			lines = append(lines, line)
		}
	}
	if interactive {
		for len(lines) < height-1 {
			lines = append(lines, "")
		}
		if height > 1 {
			lines = append(lines, ansi.Truncate(controls, width, ""))
		}
	}
	content := strings.Join(lines, "\r\n")
	if interactive {
		for i := range lines {
			lines[i] = "\x1b[2K" + lines[i]
		}
		content = "\x1b[0m\x1b[H" + strings.Join(lines, "\r\n")
		if h.renderedWidth != width || h.renderedHeight != height {
			content = "\x1b[2J" + content
		}
		h.renderedWidth, h.renderedHeight = width, height
	} else {
		content += "\r\n"
	}
	return transfer.WriteFull(h.cfg.Output, []byte(content))
}
