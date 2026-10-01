package hexdata

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const maxFrameBytes = 65536

type FrameConfig struct {
	Kind                    string
	Size                    int
	Delimiter               []byte
	Offset, Width, Overhead int
	LittleEndian            bool
}

// ParseFrameConfig is the boundary between command strings and the stream decoder.
// Length framing uses total_size = payload_length + overhead, including the header
// and trailer. Delimiters are included in returned frames.
func ParseFrameConfig(spec string) (FrameConfig, error) {
	parts := strings.Split(spec, ":")
	bad := func() (FrameConfig, error) {
		return FrameConfig{}, fmt.Errorf("invalid frame rule %q; use chunk, fixed:N, delimiter:HEX, length:OFFSET:WIDTH:OVERHEAD:le|be, or modbus-read", spec)
	}
	switch parts[0] {
	case "", "chunk":
		if len(parts) != 1 {
			return bad()
		}
		return FrameConfig{Kind: "chunk"}, nil
	case "fixed":
		if len(parts) != 2 {
			return bad()
		}
		size, err := strconv.Atoi(parts[1])
		if err != nil || size < 1 || size > maxFrameBytes {
			return bad()
		}
		return FrameConfig{Kind: "fixed", Size: size}, nil
	case "delimiter":
		if len(parts) != 2 {
			return bad()
		}
		delimiter, err := Parse(parts[1])
		if err != nil || len(delimiter) == 0 || len(delimiter) > 256 {
			return bad()
		}
		return FrameConfig{Kind: "delimiter", Delimiter: delimiter}, nil
	case "length":
		if len(parts) != 5 {
			return bad()
		}
		offset, e1 := strconv.Atoi(parts[1])
		width, e2 := strconv.Atoi(parts[2])
		overhead, e3 := strconv.Atoi(parts[3])
		if e1 != nil || e2 != nil || e3 != nil || offset < 0 || (width != 1 && width != 2 && width != 4) || offset > maxFrameBytes-width || overhead < offset+width || overhead > maxFrameBytes || (parts[4] != "le" && parts[4] != "be") {
			return bad()
		}
		return FrameConfig{Kind: "length", Offset: offset, Width: width, Overhead: overhead, LittleEndian: parts[4] == "le"}, nil
	case "modbus-read":
		if len(parts) != 1 {
			return bad()
		}
		return FrameConfig{Kind: "modbus-read"}, nil
	}
	return bad()
}

type Framer struct {
	cfg     FrameConfig
	pending []byte
}

func NewFramer(cfg FrameConfig) *Framer { return &Framer{cfg: cfg} }
func (f *Framer) Pending() int          { return len(f.pending) }
func (f *Framer) Reset() int            { count := len(f.pending); f.pending = nil; return count }

// Push reassembles a stream independent of serial Read boundaries. On invalid
// framing the incomplete buffer is reset and the caller must report the error.
func (f *Framer) Push(data []byte) ([][]byte, error) {
	if f.cfg.Kind == "" || f.cfg.Kind == "chunk" {
		if len(data) == 0 {
			return nil, nil
		}
		return [][]byte{append([]byte(nil), data...)}, nil
	}
	var frames [][]byte
	// Feed bounded pieces so arbitrarily large input cannot grow the pending buffer.
	for len(data) > 0 {
		space := maxFrameBytes - len(f.pending)
		if space == 0 {
			f.Reset()
			return frames, errors.New("incomplete frame exceeded 65536 bytes; buffer reset")
		}
		n := min(space, len(data))
		f.pending = append(f.pending, data[:n]...)
		data = data[n:]
		for {
			size, err := f.nextSize()
			if err != nil {
				f.Reset()
				return frames, err
			}
			if size == 0 || len(f.pending) < size {
				break
			}
			frames = append(frames, append([]byte(nil), f.pending[:size]...))
			f.pending = f.pending[size:]
		}
	}
	return frames, nil
}

func (f *Framer) nextSize() (int, error) {
	switch f.cfg.Kind {
	case "fixed":
		return f.cfg.Size, nil
	case "delimiter":
		if index := bytes.Index(f.pending, f.cfg.Delimiter); index >= 0 {
			return index + len(f.cfg.Delimiter), nil
		}
	case "length":
		if len(f.pending) < f.cfg.Offset+f.cfg.Width {
			return 0, nil
		}
		field := f.pending[f.cfg.Offset : f.cfg.Offset+f.cfg.Width]
		var order binary.ByteOrder = binary.BigEndian
		if f.cfg.LittleEndian {
			order = binary.LittleEndian
		}
		var payload uint64
		switch f.cfg.Width {
		case 1:
			payload = uint64(field[0])
		case 2:
			payload = uint64(order.Uint16(field))
		case 4:
			payload = uint64(order.Uint32(field))
		}
		if payload > uint64(maxFrameBytes-f.cfg.Overhead) {
			return 0, errors.New("length field exceeds 65536-byte frame limit; buffer reset")
		}
		return int(payload) + f.cfg.Overhead, nil
	case "modbus-read":
		if len(f.pending) < 2 {
			return 0, nil
		}
		if f.pending[1] == 0x83 || f.pending[1] == 0x84 {
			return 5, nil
		}
		if f.pending[1] != 3 && f.pending[1] != 4 {
			return 0, errors.New("modbus-read expects function 03/04 or exception 83/84; buffer reset")
		}
		if len(f.pending) < 3 {
			return 0, nil
		}
		if f.pending[2] == 0 || f.pending[2] > 250 || f.pending[2]%2 != 0 {
			return 0, errors.New("invalid Modbus register byte count; buffer reset")
		}
		return int(f.pending[2]) + 5, nil
	}
	return 0, nil
}
