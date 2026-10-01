package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/transfer"
)

var ErrDisconnected = errors.New("serial port is disconnected")
var ErrStaleConnection = errors.New("serial connection changed before writing")

const DefaultReconnectInterval = time.Second

type Port interface{ io.ReadWriteCloser }

type Event interface{ isBackendEvent() }

type Received struct {
	Data []byte
	At   time.Time
}

func (Received) isBackendEvent() {}

type Disconnected struct{ Err error }

func (Disconnected) isBackendEvent() {}

type Reconnecting struct {
	Attempt int
	Err     error
}

func (Reconnecting) isBackendEvent() {}

type Reconnected struct{ Attempt int }

func (Reconnected) isBackendEvent() {}

type Observer interface {
	ConnectionChanged(uint64, bool)
	Received(uint64, []byte)
	Written(uint64, int)
}

type Config struct {
	Observer          Observer
	Port              Port
	Reconnect         func() (Port, error)
	ReconnectInterval time.Duration
}

type Session struct{ cfg Config }

func New(cfg Config) *Session { return &Session{cfg: cfg} }

type writeRequest struct {
	ctx          context.Context
	generation   uint64
	checkpoint   func()
	checkpointMu sync.Mutex
	active       bool
	data         []byte
	done         chan error
}
type reconfigureRequest struct {
	open func() (Port, error)
	done chan error
}
type connectionFailure struct {
	generation uint64
	err        error
}

type Endpoint struct {
	ctx         context.Context
	cancel      context.CancelFunc
	events      chan Event
	writes      chan *writeRequest
	reconfigure chan reconfigureRequest

	portMu     sync.RWMutex
	port       Port
	generation uint64
	observer   Observer
}

func (e *Endpoint) Events() <-chan Event { return e.events }

func (e *Endpoint) Send(ctx context.Context, data []byte) error {
	_, generation := e.connection()
	return e.SendGeneration(ctx, generation, data, nil)
}

// A queued request carries the connection generation it was submitted against.
// Validate it in runWriter, never only at enqueue time.
func (e *Endpoint) SendGeneration(ctx context.Context, generation uint64, data []byte, checkpoint func()) error {
	req := &writeRequest{ctx: ctx, generation: generation, data: append([]byte(nil), data...), done: make(chan error, 1), checkpoint: checkpoint, active: true}
	defer func() { req.checkpointMu.Lock(); req.active = false; req.checkpointMu.Unlock() }()
	select {
	case e.writes <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-e.ctx.Done():
		return context.Canceled
	}
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-e.ctx.Done():
		return context.Canceled
	}
}

func (e *Endpoint) Quit() { e.cancel() }

func (e *Endpoint) Reconfigure(ctx context.Context, open func() (Port, error)) error {
	if open == nil {
		return errors.New("serial port opener is nil")
	}
	req := reconfigureRequest{open: open, done: make(chan error, 1)}
	select {
	case e.reconfigure <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-e.ctx.Done():
		return context.Canceled
	}
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-e.ctx.Done():
		return context.Canceled
	}
}

func (e *Endpoint) connection() (Port, uint64) {
	e.portMu.RLock()
	defer e.portMu.RUnlock()
	return e.port, e.generation
}

func (e *Endpoint) setConnection(port Port, generation uint64) {
	e.portMu.Lock()
	e.port, e.generation = port, generation
	if e.observer != nil {
		e.observer.ConnectionChanged(generation, port != nil)
	}
	e.portMu.Unlock()
}

func (e *Endpoint) emit(event Event) bool {
	select {
	case e.events <- event:
		return true
	case <-e.ctx.Done():
		return false
	}
}

func (s *Session) Run(parent context.Context, ready chan<- *Endpoint) error {
	if s.cfg.Port == nil {
		return errors.New("serial port is nil")
	}
	ctx, cancel := context.WithCancel(parent)
	e := &Endpoint{
		ctx: ctx, cancel: cancel, events: make(chan Event, 32),
		observer: s.cfg.Observer, writes: make(chan *writeRequest), reconfigure: make(chan reconfigureRequest),
	}
	e.setConnection(&ownedPort{Port: s.cfg.Port}, 1)
	select {
	case ready <- e:
	case <-ctx.Done():
		cancel()
		e.setConnection(nil, 1)
		if err := s.cfg.Port.Close(); err != nil {
			return fmt.Errorf("close serial port: %w", err)
		}
		return nil
	}

	failures := make(chan connectionFailure, 2)
	writerDone := make(chan error, 1)
	go func() { writerDone <- s.runWriter(ctx, e, failures) }()
	connectionsDone := make(chan error, 1)
	go func() { connectionsDone <- s.runConnections(ctx, e, failures) }()

	var runErr error
	connectionsReturned := false
	writerReturned := false
	select {
	case runErr = <-connectionsDone:
		connectionsReturned = true
	case runErr = <-writerDone:
		writerReturned = true
	case <-ctx.Done():
	case <-parent.Done():
	}
	cancel()
	port, _ := e.connection()
	if port != nil {
		_ = port.Close()
	}
	if !connectionsReturned {
		if err := <-connectionsDone; runErr == nil {
			runErr = err
		}
	}
	if !writerReturned {
		if err := <-writerDone; runErr == nil {
			runErr = err
		}
	}
	close(e.events)
	return normalizeError(runErr)
}

func (s *Session) runWriter(ctx context.Context, e *Endpoint, failures chan<- connectionFailure) error {
	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		case req := <-e.writes:
			if err := req.ctx.Err(); err != nil {
				req.done <- err
				continue
			}
			e.portMu.RLock()
			port, generation := e.port, e.generation
			if generation != req.generation {
				e.portMu.RUnlock()
				req.done <- ErrStaleConnection
				continue
			}
			req.checkpointMu.Lock()
			if req.active && req.checkpoint != nil && port != nil {
				req.checkpoint()
			}
			req.checkpointMu.Unlock()
			e.portMu.RUnlock()
			if port == nil {
				req.done <- ErrDisconnected
				continue
			}
			if err := transfer.WriteFull(&observedWriter{ctx: req.ctx, port: port, observer: e.observer, generation: generation}, req.data); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					req.done <- err
					continue
				}
				req.done <- fmt.Errorf("%w: %w", ErrDisconnected, err)
				if errors.Is(err, capture.ErrRecording) {
					return err
				}
				select {
				case failures <- connectionFailure{generation, fmt.Errorf("write serial port: %w", err)}:
				case <-ctx.Done():
					return context.Canceled
				}
				continue
			}
			req.done <- nil
		}
	}
}

func (s *Session) runConnections(ctx context.Context, e *Endpoint, failures chan connectionFailure) error {
	port, generation := e.connection()
	open := s.cfg.Reconnect
	readerDone := s.startReader(ctx, e, port, generation, failures)
	for {
		select {
		case <-ctx.Done():
			e.setConnection(nil, generation)
			closeErr := port.Close()
			<-readerDone
			if closeErr != nil {
				return fmt.Errorf("close serial port: %w", closeErr)
			}
			return context.Canceled
		case req := <-e.reconfigure:
			e.setConnection(nil, generation)
			_ = port.Close()
			<-readerDone

			newPort, err := req.open()
			if err != nil {
				if open == nil {
					req.done <- err
					return fmt.Errorf("apply serial configuration: %w", err)
				}
				restoredPort, restoreErr := open()
				if restoreErr != nil {
					req.done <- errors.Join(err, restoreErr)
					return fmt.Errorf("apply serial configuration: %w", errors.Join(err, restoreErr))
				}
				port = &ownedPort{Port: restoredPort}
				generation++
				e.setConnection(port, generation)
				readerDone = s.startReader(ctx, e, port, generation, failures)
				req.done <- err
				continue
			}

			port = &ownedPort{Port: newPort}
			open = req.open
			generation++
			e.setConnection(port, generation)
			readerDone = s.startReader(ctx, e, port, generation, failures)
			req.done <- nil
		case failure := <-failures:
			if failure.generation != generation {
				continue
			}
			e.setConnection(nil, generation)
			_ = port.Close()
			<-readerDone
			if errors.Is(failure.err, capture.ErrRecording) {
				return failure.err
			}
			e.emit(Disconnected{Err: failure.err})
			newPort, newOpen, attempt, configured, err := s.reconnect(ctx, e, open, failure.err)
			if err != nil {
				return err
			}
			port = &ownedPort{Port: newPort}
			open = newOpen
			generation++
			e.setConnection(port, generation)
			e.emit(Reconnected{Attempt: attempt})
			readerDone = s.startReader(ctx, e, port, generation, failures)
			if configured != nil {
				configured <- nil
			}
		}
	}
}

func (s *Session) startReader(ctx context.Context, e *Endpoint, port Port, generation uint64, failures chan<- connectionFailure) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := port.Read(buf)
			if n > 0 {
				if e.observer != nil {
					e.observer.Received(generation, buf[:n])
				}
				if !e.emit(Received{Data: append([]byte(nil), buf[:n]...), At: time.Now()}) {
					return
				}
			}
			if err != nil {
				select {
				case failures <- connectionFailure{generation, fmt.Errorf("read serial port: %w", err)}:
				case <-ctx.Done():
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()
	return done
}

func (s *Session) reconnect(ctx context.Context, e *Endpoint, open func() (Port, error), cause error) (Port, func() (Port, error), int, chan error, error) {
	if open == nil {
		return nil, open, 0, nil, cause
	}
	interval := s.cfg.ReconnectInterval
	if interval <= 0 {
		interval = DefaultReconnectInterval
	}
	lastErr := cause
	for attempt := 1; ; {
		e.emit(Reconnecting{Attempt: attempt, Err: lastErr})
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
			port, err := open()
			if err == nil {
				return port, open, attempt, nil, nil
			}
			lastErr = err
			attempt++
		case req := <-e.reconfigure:
			timer.Stop()
			port, err := req.open()
			if err == nil {
				return port, req.open, attempt, req.done, nil
			}
			req.done <- err
			lastErr = err
		case <-ctx.Done():
			timer.Stop()
			return nil, open, attempt - 1, nil, context.Canceled
		}
	}
}

func normalizeError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// Counts actual short writes, including partial failures and transfer traffic.
type observedWriter struct {
	ctx        context.Context
	port       Port
	observer   Observer
	generation uint64
}

func (w *observedWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.port.Write(data)
	if n > 0 && w.observer != nil {
		w.observer.Written(w.generation, n)
	}
	return n, err
}
