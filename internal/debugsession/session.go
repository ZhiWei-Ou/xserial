package debugsession

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
)

type connection struct {
	cfg      Connection
	history  *history
	endpoint middleware.Endpoint
	done     chan struct{}
	runErr   error  // Published by closing done.
	closed   bool   // Protected by Service.mu.
	txBytes  uint64 // Protected by Service.mu.
}

type Service struct {
	ctx     context.Context
	cancel  context.CancelFunc
	deps    Dependencies
	control sync.Mutex // Serializes control changes and complete sends.
	mu      sync.Mutex
	active  *connection
	owner   string // IPC connection identity; empty permits a new controller.
}

func New(ctx context.Context, deps Dependencies) *Service {
	ctx, cancel := context.WithCancel(ctx)
	return &Service{ctx: ctx, cancel: cancel, deps: deps}
}

func (s *Service) List() (ListResult, error) {
	infos, err := s.deps.List()
	result := ListResult{Ports: make([]PortInfo, 0, len(infos))}
	for _, info := range infos {
		result.Ports = append(result.Ports, PortInfo{info.Name, info.Product, info.VID, info.PID, info.SerialNumber})
	}
	return result, err
}

func normalize(cfg Connection) (Connection, error) {
	if cfg.Port == "" {
		return cfg, invalid("port is required")
	}
	if cfg.Baud == 0 {
		cfg.Baud = 115200
	}
	if cfg.DataBits == 0 {
		cfg.DataBits = 8
	}
	if cfg.Parity == "" {
		cfg.Parity = "N"
	}
	cfg.Parity = strings.ToUpper(cfg.Parity)
	if cfg.StopBits == "" {
		cfg.StopBits = "1"
	}
	if cfg.Baud < 1 || cfg.DataBits < 5 || cfg.DataBits > 8 ||
		!strings.Contains("NOEMS", cfg.Parity) || len(cfg.Parity) != 1 ||
		(cfg.StopBits != "1" && cfg.StopBits != "1.5" && cfg.StopBits != "2") {
		return cfg, invalid("invalid serial configuration")
	}
	return cfg, nil
}

func (s *Service) Open(ctx context.Context, owner string, cfg Connection) (OpenResult, error) {
	cfg, err := normalize(cfg)
	if err != nil {
		return OpenResult{}, err
	}
	s.control.Lock()
	defer s.control.Unlock()
	if err := ctx.Err(); err != nil {
		return OpenResult{}, err
	}
	if err := s.ctx.Err(); err != nil {
		return OpenResult{}, err
	}
	s.mu.Lock()
	c := s.active
	if s.owner != "" && s.owner != owner {
		s.mu.Unlock()
		return OpenResult{}, fault("busy", "another MCP client controls the serial session")
	}
	if c != nil && !c.closed {
		select {
		case <-c.done:
			// A disconnected session can be explicitly reopened, with a new ID.
		default:
			if cfg != c.cfg {
				s.mu.Unlock()
				return OpenResult{}, fault("connection_active", "close the existing serial session before opening a different connection")
			}
			s.owner = owner
			s.mu.Unlock()
			return OpenResult{c.history.id, c.history.head(), true, cfg}, nil
		}
	}
	s.mu.Unlock()
	port, err := s.deps.Open(ctx, cfg)
	if err != nil {
		return OpenResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return OpenResult{}, errors.Join(err, port.Close())
	}
	id := rand.Text()
	c = &connection{cfg: cfg, history: newHistory(id, HistoryBytes), done: make(chan struct{})}
	ready := make(chan middleware.Endpoint, 1)
	frontend := &collector{history: c.history, ready: ready}
	go func() {
		c.runErr = middleware.New(middleware.Config{Port: port, Frontend: frontend}).Run(s.ctx)
		c.history.finish("disconnected", c.runErr)
		close(c.done)
	}()
	select {
	case c.endpoint = <-ready:
	case <-c.done:
		return OpenResult{}, errors.Join(fault("open_failed", "serial session failed to start"), c.runErr)
	case <-ctx.Done():
		// The ready endpoint is still delivered, so shutdown can be synchronized.
		select {
		case e := <-ready:
			e.Quit()
		case <-c.done:
		}
		<-c.done
		return OpenResult{}, ctx.Err()
	}
	s.mu.Lock()
	s.active, s.owner = c, owner
	s.mu.Unlock()
	// Start at zero to include bytes received immediately after opening.
	return OpenResult{id, c.history.cursor(0), false, cfg}, nil
}

type collector struct {
	history *history
	ready   chan<- middleware.Endpoint
}

func (f *collector) Run(_ context.Context, e middleware.Endpoint) error {
	events := e.Events()
	f.ready <- e
	// Session closes Events before waiting for this frontend. Drain queued
	// bytes even after cancellation so a device's final output is retained.
	for event := range events {
		switch event := event.(type) {
		case middleware.Received:
			f.history.append(event.Data)
		case middleware.Disconnected:
			f.history.finish("disconnected", event.Err)
		}
	}
	return nil
}

func (s *Service) session(id string, owner string) (*connection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || s.active.history.id != id {
		return nil, fault("stale_session", "serial session does not exist; use serial_open to obtain its ID")
	}
	if owner != "" && s.owner != owner {
		return nil, fault("not_controller", "use serial_open to acquire control; another client may already own it")
	}
	return s.active, nil
}

func (s *Service) Send(ctx context.Context, owner string, in SendInput) (SendResult, error) {
	timeout := 5000
	if in.TimeoutMS != nil {
		timeout = *in.TimeoutMS
	}
	if timeout < 1 || timeout > 30000 {
		return SendResult{}, invalid("timeout_ms must be 1 through 30000")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	if len(in.Data) > 256<<10 {
		return SendResult{}, invalid("send data exceeds 256 KiB encoded limit")
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
		return SendResult{}, invalid("encoding must be text, hex or base64")
	}
	if err != nil {
		return SendResult{}, invalid("decode send data: %v", err)
	}
	if len(data) == 0 || len(data) > 65536 {
		return SendResult{}, invalid("send must contain 1 through 65536 bytes")
	}
	s.control.Lock()
	defer s.control.Unlock()
	if err := ctx.Err(); err != nil {
		return SendResult{}, err
	}
	c, err := s.session(in.SessionID, owner)
	if err != nil {
		return SendResult{}, err
	}
	result := SendResult{SessionID: in.SessionID, Cursor: c.history.head(), Delivery: "unknown"}
	select {
	case <-c.done:
		return result, fault("disconnected", "serial device is disconnected")
	default:
	}
	if err := c.endpoint.Send(ctx, data); err != nil {
		return result, err
	}
	s.mu.Lock()
	c.txBytes += uint64(len(data))
	s.mu.Unlock()
	result.Bytes, result.Delivery = len(data), "written"
	return result, nil
}

func (s *Service) Read(ctx context.Context, in ReadInput) (ReadResult, error) {
	c, err := s.session(in.SessionID, "")
	if err != nil {
		return ReadResult{}, err
	}
	return c.history.read(ctx, in)
}

func (s *Service) Status(owner string) StatusResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := StatusResult{State: "closed", Control: "available"}
	if s.owner == owner && owner != "" {
		result.Control = "owned"
	} else if s.owner != "" {
		result.Control = "busy"
	}
	if c := s.active; c != nil {
		c.history.mu.Lock()
		defer c.history.mu.Unlock()
		cfg := c.cfg
		result.SessionID, result.Connection = c.history.id, &cfg
		result.State, result.Error = c.history.state, c.history.err
		result.RXBytes, result.TXBytes = c.history.end, c.txBytes
		result.Cursor = c.history.cursor(c.history.end)
	}
	return result
}

func (s *Service) Close(ctx context.Context, owner string, in SessionInput) (CloseResult, error) {
	s.control.Lock()
	defer s.control.Unlock()
	if err := ctx.Err(); err != nil {
		return CloseResult{}, err
	}
	c, err := s.session(in.SessionID, "")
	if err != nil {
		return CloseResult{}, err
	}
	s.mu.Lock()
	closed, controller := c.closed, s.owner
	s.mu.Unlock()
	if closed {
		return CloseResult{true}, nil
	}
	if controller != owner {
		return CloseResult{}, fault("not_controller", "use serial_open to acquire control before closing")
	}
	c.endpoint.Quit()
	<-c.done
	c.history.finish("closed", c.runErr)
	s.mu.Lock()
	c.closed, s.owner = true, ""
	s.mu.Unlock()
	return CloseResult{true}, errors.Join(c.runErr, ctx.Err())
}

// Release drops controller ownership after IPC disconnect, preserving the port
// and receive history so a replacement coding client can resume debugging.
func (s *Service) Release(owner string) {
	s.control.Lock()
	defer s.control.Unlock()
	s.mu.Lock()
	if s.owner == owner {
		s.owner = ""
	}
	s.mu.Unlock()
}

func (s *Service) Shutdown() error {
	s.cancel()
	s.control.Lock()
	defer s.control.Unlock()
	s.mu.Lock()
	c := s.active
	s.mu.Unlock()
	if c == nil {
		return nil
	}
	<-c.done
	return c.runErr
}
