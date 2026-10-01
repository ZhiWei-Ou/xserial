package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/transfer"
)

var ErrDisconnected = errors.New("serial port is disconnected")

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

type Config struct {
	Port              Port
	Reconnect         func() (Port, error)
	ReconnectInterval time.Duration
}

type Session struct{ cfg Config }

func New(cfg Config) *Session { return &Session{cfg: cfg} }

type writeRequest struct {
	data []byte
	done chan error
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
	writes      chan writeRequest
	reconfigure chan reconfigureRequest

	portMu     sync.RWMutex
	port       Port
	generation uint64
}

func (e *Endpoint) Events() <-chan Event { return e.events }

func (e *Endpoint) Send(ctx context.Context, data []byte) error {
	req := writeRequest{data: append([]byte(nil), data...), done: make(chan error, 1)}
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
		writes: make(chan writeRequest), reconfigure: make(chan reconfigureRequest),
	}
	e.setConnection(&ownedPort{Port: s.cfg.Port}, 1)
	select {
	case ready <- e:
	case <-ctx.Done():
		cancel()
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
			port, generation := e.connection()
			if port == nil {
				req.done <- ErrDisconnected
				continue
			}
			if err := transfer.WriteFull(port, req.data); err != nil {
				req.done <- fmt.Errorf("%w: %w", ErrDisconnected, err)
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
			if n > 0 && !e.emit(Received{Data: append([]byte(nil), buf[:n]...), At: time.Now()}) {
				return
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
