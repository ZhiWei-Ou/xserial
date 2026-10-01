package debugsession

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// One session collector appends. Readers observe snapshots without consuming
// bytes. Closing changed wakes every reader; its replacement is never sent to.
type history struct {
	mu       sync.Mutex
	id       string
	data     []byte
	end      uint64
	lastData time.Time
	state    string
	err      string
	changed  chan struct{}
}

func newHistory(id string, capacity int) *history {
	return &history{id: id, data: make([]byte, capacity), state: "connected", changed: make(chan struct{})}
}

func (h *history) append(data []byte) {
	if len(data) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	end := h.end + uint64(len(data))
	if len(data) > len(h.data) {
		data = data[len(data)-len(h.data):]
	}
	start := (end - uint64(len(data))) % uint64(len(h.data))
	n := copy(h.data[start:], data)
	copy(h.data, data[n:])
	h.end = end
	h.lastData = time.Now()
	h.notify()
}

func (h *history) finish(state string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = state
	if err != nil {
		h.err = err.Error()
	}
	h.notify()
}

func (h *history) notify()                     { close(h.changed); h.changed = make(chan struct{}) }
func (h *history) cursor(offset uint64) string { return fmt.Sprintf("%s:%d", h.id, offset) }
func (h *history) head() string                { h.mu.Lock(); defer h.mu.Unlock(); return h.cursor(h.end) }

func readOptions(in ReadInput) (wait, idle time.Duration, maxBytes int, err error) {
	w, i, n := 2000, 150, 8192
	if in.WaitMS != nil {
		w = *in.WaitMS
	}
	if in.IdleMS != nil {
		i = *in.IdleMS
	}
	if in.MaxBytes != nil {
		n = *in.MaxBytes
	}
	if w < 0 || w > 30000 || i < 0 || i > 5000 || n < 1 || n > 65536 {
		return 0, 0, 0, invalid("wait_ms must be 0..30000, idle_ms 0..5000 and max_bytes 1..65536")
	}
	return time.Duration(w) * time.Millisecond, time.Duration(i) * time.Millisecond, n, nil
}

func (h *history) read(ctx context.Context, in ReadInput) (ReadResult, error) {
	wait, idle, maxBytes, err := readOptions(in)
	if err != nil {
		return ReadResult{}, err
	}
	deadline := time.Now().Add(wait)
	h.mu.Lock()
	var offset uint64
	switch in.Cursor {
	case "now":
		offset = h.end
	case "":
		if h.end > uint64(maxBytes) {
			offset = h.end - uint64(maxBytes)
		}
	default:
		prefix := h.id + ":"
		if !strings.HasPrefix(in.Cursor, prefix) {
			h.mu.Unlock()
			return ReadResult{}, fault("stale_cursor", "cursor belongs to a different serial session")
		}
		offset, err = strconv.ParseUint(strings.TrimPrefix(in.Cursor, prefix), 10, 64)
		if err != nil || offset > h.end {
			h.mu.Unlock()
			return ReadResult{}, invalid("cursor is malformed or ahead of received data")
		}
	}
	h.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return ReadResult{}, err
		}
		h.mu.Lock()
		oldest := uint64(0)
		if h.end > uint64(len(h.data)) {
			oldest = h.end - uint64(len(h.data))
		}
		lost := uint64(0)
		if offset < oldest {
			lost = oldest - offset
			offset = oldest
		}
		available := h.end - offset
		n := int(min(available, uint64(maxBytes)))
		raw := make([]byte, n)
		start := offset % uint64(len(h.data))
		copied := copy(raw, h.data[start:])
		copy(raw[copied:], h.data)
		result := ReadResult{SessionID: h.id, Bytes: n, NextCursor: h.cursor(offset + uint64(n)),
			HasMore: available > uint64(n), DroppedBytes: lost, State: h.state, Error: h.err}
		lastData, changed := h.lastData, h.changed
		h.mu.Unlock()
		now := time.Now()
		switch {
		case lost > 0:
			result.Reason = "gap"
		case n == maxBytes:
			result.Reason = "limit"
		case result.State != "connected":
			result.Reason = result.State
		case wait == 0:
			result.Reason = "snapshot"
		case !now.Before(deadline):
			result.Reason = "deadline"
		case n > 0 && !now.Before(lastData.Add(idle)):
			result.Reason = "idle"
		}
		if result.Reason != "" {
			result.DataBase64 = base64.StdEncoding.EncodeToString(raw)
			result.Output = display(raw)
			return result, nil
		}
		wake := deadline
		if n > 0 && lastData.Add(idle).Before(wake) {
			wake = lastData.Add(idle)
		}
		timer := time.NewTimer(time.Until(wake))
		select {
		case <-changed:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ReadResult{}, ctx.Err()
		}
		timer.Stop()
	}
}

// Output is a readable transcript, not a VT screen. Base64 remains authoritative
// when controls, invalid UTF-8 or a rune split by max_bytes affect presentation.
func display(data []byte) string {
	text := ansi.Strip(strings.ToValidUTF8(string(data), "\uFFFD"))
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var out strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			fmt.Fprintf(&out, "\\x%02x", r)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
