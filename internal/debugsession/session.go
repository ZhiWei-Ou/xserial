package debugsession

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
)

// Sender validates generation at the serial writer boundary. checkpoint is
// called there before writing, and never after Sender returns.
type Sender func(ctx context.Context, generation uint64, data []byte, checkpoint func()) error

// Session observes a terminal-owned connection. It owns only bounded history;
// no IPC reader or daemon is involved in the device's receive path.
type Session struct {
	mu         sync.Mutex
	cfg        Connection
	generation uint64
	history    *history
	txBytes    uint64
	sender     Sender
}

func New(cfg Connection) *Session { return &Session{cfg: cfg} }

func (s *Session) SetSender(sender Sender)  { s.mu.Lock(); s.sender = sender; s.mu.Unlock() }
func (s *Session) SetConfig(cfg Connection) { s.mu.Lock(); s.cfg = cfg; s.mu.Unlock() }

// ConnectionChanged runs synchronously with connection replacement. Finishing
// old history wakes readers holding it even after a new identity exists.
func (s *Session) ConnectionChanged(generation uint64, connected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history != nil {
		s.history.finish("detached", nil)
	}
	s.history = nil
	s.generation = generation
	s.txBytes = 0
	if connected {
		s.history = newHistory(rand.Text(), HistoryBytes)
		s.history.connection = s.cfg
	}
}

func (s *Session) Received(generation uint64, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history != nil && s.generation == generation {
		s.history.append(data)
	}
}

func (s *Session) Written(generation uint64, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history != nil && s.generation == generation {
		s.txBytes += uint64(n)
	}
}

func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history == nil {
		return Status{State: "detached"}
	}
	h := s.history
	h.mu.Lock()
	defer h.mu.Unlock()
	cfg := h.connection
	return Status{SessionID: h.id, State: h.state, Connection: &cfg,
		RXBytes: h.end, TXBytes: s.txBytes, Cursor: h.cursor(h.end), Error: h.err}
}

func (s *Session) lookup(id string) (*history, uint64, Sender, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.history == nil || id == "" || s.history.id != id {
		return nil, 0, nil, fault("detached", "serial session is detached; use serial_status to obtain a current session ID")
	}
	return s.history, s.generation, s.sender, nil
}

func DecodeSend(in SendInput) ([]byte, time.Duration, error) {
	timeout := 5000
	if in.TimeoutMS != nil {
		timeout = *in.TimeoutMS
	}
	if timeout < 1 || timeout > 30000 {
		return nil, 0, invalid("timeout_ms must be 1 through 30000")
	}
	if len(in.Data) > 256<<10 {
		return nil, 0, invalid("send data exceeds 256 KiB encoded limit")
	}
	var data []byte
	var err error
	switch in.Encoding {
	case "", "text":
		data = []byte(in.Data)
	case "hex":
		data, err = hexdata.Parse(in.Data)
	case "base64":
		data, err = base64.StdEncoding.DecodeString(in.Data)
	default:
		return nil, 0, invalid("encoding must be text, hex or base64")
	}
	if err != nil {
		return nil, 0, invalid("decode send data: %v", err)
	}
	if len(data) == 0 || len(data) > 65536 {
		return nil, 0, invalid("send must contain 1 through 65536 bytes")
	}
	return data, time.Duration(timeout) * time.Millisecond, nil
}

func (s *Session) Send(ctx context.Context, in SendInput) (SendResult, error) {
	data, timeout, err := DecodeSend(in)
	if err != nil {
		return SendResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	h, generation, sender, err := s.lookup(in.SessionID)
	if err != nil {
		return SendResult{}, err
	}
	result := SendResult{SessionID: in.SessionID, Cursor: h.head(), Delivery: "unknown"}
	if sender == nil {
		return result, fault("detached", "terminal session is starting or stopping")
	}
	// Retain a checkpoint even when cancellation prevents entering the writer.
	// The boundary checkpoint includes output received while the send was queued.
	var mu sync.Mutex
	err = sender(ctx, generation, data, func() { mu.Lock(); result.Cursor = h.head(); mu.Unlock() })
	mu.Lock()
	defer mu.Unlock()
	if err == nil {
		result.Bytes, result.Delivery = len(data), "written"
	}
	return result, err
}

func (s *Session) Read(ctx context.Context, in ReadInput) (ReadResult, error) {
	h, _, _, err := s.lookup(in.SessionID)
	if err != nil {
		return ReadResult{}, err
	}
	return h.read(ctx, in)
}
