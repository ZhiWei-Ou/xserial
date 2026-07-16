package session

import (
	"context"
	"fmt"
	"time"
)

const defaultReconnectInterval = time.Second

type connectionFailure struct {
	generation uint64
	err        error
}

func (s *Session) runConnections(ctx context.Context, e *endpoint, failures chan connectionFailure) error {
	port, generation := e.connection()
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
		case failure := <-failures:
			if failure.generation != generation {
				continue
			}

			e.setConnection(nil, generation)
			e.CancelTransfer()
			_ = port.Close()
			<-readerDone
			e.emit(Disconnected{Err: failure.err})

			if s.cfg.Reconnect == nil {
				e.logWarn("session.disconnected", "error", failure.err)
				return failure.err
			}
			e.logWarn("session.disconnected", "error", failure.err, "retry_in", s.reconnectInterval())
			var (
				err     error
				attempt int
			)
			port, attempt, err = s.reconnect(ctx, e)
			if err != nil {
				return err
			}
			generation++
			e.setConnection(port, generation)
			e.logInfo("session.reconnected", "attempt", attempt)
			e.emit(Reconnected{})
			readerDone = s.startReader(ctx, e, port, generation, failures)
		}
	}
}

func (s *Session) startReader(
	ctx context.Context,
	e *endpoint,
	port SerialPort,
	generation uint64,
	failures chan<- connectionFailure,
) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := s.runReader(ctx, e, port)
		select {
		case failures <- connectionFailure{generation: generation, err: err}:
		case <-ctx.Done():
		}
	}()
	return done
}

func (s *Session) reconnect(ctx context.Context, e *endpoint) (SerialPort, int, error) {
	interval := s.reconnectInterval()
	attempt := 0
	for {
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, attempt, context.Canceled
		}

		attempt++
		port, err := s.cfg.Reconnect()
		if err == nil {
			return port, attempt, nil
		}
		e.logWarn("session.reconnect_failed", "attempt", attempt, "error", err, "retry_in", interval)
	}
}

func (s *Session) reconnectInterval() time.Duration {
	if s.cfg.ReconnectInterval > 0 {
		return s.cfg.ReconnectInterval
	}
	return defaultReconnectInterval
}
