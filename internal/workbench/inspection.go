package workbench

import (
	"bytes"
	"fmt"
	"unicode"

	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
)

func (m *model) insertName(text string) {
	for _, r := range text {
		if !unicode.IsControl(r) && len(m.name) < 80 {
			m.name = append(m.name, r)
		}
	}
}

func (m *model) selection() []byte {
	if len(m.entries) == 0 {
		return nil
	}
	m.selected = min(max(0, m.selected), len(m.entries)-1)
	data := m.entries[m.selected].Data
	if len(data) == 0 {
		return nil
	}
	m.selectionOffset = min(max(0, m.selectionOffset), len(data)-1)
	return data[m.selectionOffset:min(len(data), m.selectionOffset+m.selectionWidth)]
}

func (m *model) handleInspectionKey(key string) {
	switch key {
	case "up", "k":
		m.selected = max(0, m.selected-1)
		m.selectionOffset = 0
	case "down", "j":
		m.selected = min(len(m.entries)-1, m.selected+1)
		m.selectionOffset = 0
	case "left", "h":
		m.selectionOffset = max(0, m.selectionOffset-1)
	case "right", "l":
		if len(m.entries) > 0 {
			m.selectionOffset = min(len(m.entries[m.selected].Data)-1, m.selectionOffset+1)
		}
	case "[":
		m.selectionWidth = max(1, m.selectionWidth/2)
	case "]":
		m.selectionWidth = min(8, m.selectionWidth*2)
	case "enter":
		if !m.sending {
			m.setInput(hexdata.Format(m.selection()))
			m.modal = ""
			m.status = "Selection copied to input"
		}
	case "n", "N":
		if len(m.matches) > 0 {
			m.matchIndex = (m.matchIndex + 1) % len(m.matches)
			m.selected = m.matches[m.matchIndex]
			pattern, _ := hexdata.Parse(string(m.query))
			m.selectionOffset = max(0, bytes.Index(m.entries[m.selected].Data, pattern))
		}
	}
}

func (m *model) inspectionLines() []string {
	selected := m.selection()
	if len(selected) == 0 {
		return []string{" No traffic to inspect"}
	}
	entry := m.entries[m.selected]
	kind := "data block"
	if entry.Frame {
		kind = "frame"
	}
	lines := []string{
		fmt.Sprintf(" Inspect %d/%d · %s %s · %d bytes", m.selected+1, len(m.entries), entry.Direction, kind, len(entry.Data)),
		" ↑↓ entry  ←→ offset  [ ] width  Enter copy  Ctrl-B mark  Esc close",
		fmt.Sprintf(" Offset %d · Length %d · %s", m.selectionOffset, len(selected), hexdata.Format(selected)),
		"", fmt.Sprintf(" %-10s %-22s %s", "Type", "Little endian", "Big endian"),
	}
	for _, value := range hexdata.Interpret(selected) {
		lines = append(lines, fmt.Sprintf(" %-10s %-22s %s", value.Type, value.LittleEndian, value.BigEndian))
	}
	if len(hexdata.Interpret(selected)) == 0 {
		lines = append(lines, " Select exactly 1, 2, 4, or 8 bytes to interpret a number")
	}
	lines = append(lines, "", " Entire entry checksum:")
	if entry.Note != "" {
		lines = append(lines, " Mark: "+entry.Note)
	}
	for _, algorithm := range checksumAlgorithms {
		valid, err := hexdata.VerifyChecksum(entry.Data, algorithm)
		result := "FAIL"
		if valid {
			result = "OK"
		}
		if err != nil {
			result = err.Error()
		}
		lines = append(lines, " "+algorithm+": "+result)
	}
	return lines
}

var checksumAlgorithms = []string{"crc16-modbus", "sum8", "xor8"}

func (m *model) handleChecksumKey(key string) {
	switch key {
	case "up", "k":
		m.checksumIndex = (m.checksumIndex + len(checksumAlgorithms) - 1) % len(checksumAlgorithms)
	case "down", "j":
		m.checksumIndex = (m.checksumIndex + 1) % len(checksumAlgorithms)
	case "enter":
		data, err := hexdata.Parse(string(m.input))
		if err != nil || len(data) == 0 {
			m.status = "Enter valid payload bytes before appending a checksum"
			return
		}
		algorithm := checksumAlgorithms[m.checksumIndex]
		data, _ = hexdata.AppendChecksum(data, algorithm)
		m.setInput(hexdata.Format(data))
		m.modal = ""
		m.status = "Appended " + algorithm + "; review bytes before Enter sends"
	}
}

func (m *model) checksumLines() []string {
	lines := []string{" Append checksum · ↑↓ algorithm · Enter apply · Esc close", " Payload must exclude any existing checksum; nothing is sent automatically.", ""}
	for i, algorithm := range checksumAlgorithms {
		marker := "  "
		if i == m.checksumIndex {
			marker = "> "
		}
		lines = append(lines, marker+algorithm)
	}
	lines = append(lines, "", " CRC16 Modbus: poly=0x8005 (reflected 0xA001), init=0xFFFF, xorout=0", " CRC16 Modbus bytes: low byte first. SUM8: sum modulo 256. XOR8: byte XOR.")
	data, err := hexdata.Parse(string(m.input))
	if err != nil {
		return append(lines, " "+err.Error())
	}
	packet, _ := hexdata.AppendChecksum(data, checksumAlgorithms[m.checksumIndex])
	return append(lines, fmt.Sprintf(" Preview %d bytes: %s", len(packet), hexdata.Format(packet)))
}
