package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/transfer"
)

var ErrTransferActive = errors.New("transfer is active")

type SerialPort interface {
	io.ReadWriteCloser
}

type Logger interface {
	Info(event string, keyValues ...any)
	Warn(event string, keyValues ...any)
}

type Event interface {
	isSessionEvent()
}

type Received struct {
	Data []byte
	At   time.Time
}

func (Received) isSessionEvent() {}

type UploadStarted struct {
	Path string
}

func (UploadStarted) isSessionEvent() {}

type UploadProgress struct {
	Path           string
	Written, Total int64
}

func (UploadProgress) isSessionEvent() {}

type UploadFinished struct {
	Path  string
	Bytes int64
	Err   error
}

func (UploadFinished) isSessionEvent() {}

type Endpoint interface {
	Events() <-chan Event
	Send(context.Context, []byte) error
	StartUpload(context.Context, string) error
	CancelUpload()
	Quit()
}

type Frontend interface {
	Run(context.Context, Endpoint) error
}

type Config struct {
	Port              SerialPort
	Frontend          Frontend
	ReceiveLog        io.Writer
	ReceiveTimeFormat string
	Logger            Logger
}

type Session struct {
	cfg Config
}

func New(cfg Config) *Session { return &Session{cfg: cfg} }

type writeRequest struct {
	data []byte
	done chan error
}

type endpoint struct {
	ctx    context.Context
	cancel context.CancelFunc
	events chan Event
	writes chan writeRequest
	logger Logger

	mu           sync.Mutex
	stopping     bool
	uploading    bool
	uploadCancel context.CancelFunc
	workers      sync.WaitGroup
}

func (e *endpoint) Events() <-chan Event { return e.events }

func (e *endpoint) Send(ctx context.Context, data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return context.Canceled
	}
	if e.uploading {
		return ErrTransferActive
	}
	return e.write(ctx, data)
}

func (e *endpoint) write(ctx context.Context, data []byte) error {
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

func (e *endpoint) StartUpload(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return context.Canceled
	}
	if e.uploading {
		e.mu.Unlock()
		return ErrTransferActive
	}
	uploadCtx, cancel := context.WithCancel(e.ctx)
	e.uploading = true
	e.uploadCancel = cancel
	e.workers.Add(1)
	e.mu.Unlock()

	go e.runUpload(uploadCtx, path)
	return nil
}

func (e *endpoint) runUpload(ctx context.Context, path string) {
	defer e.workers.Done()
	e.emit(UploadStarted{Path: path})

	lastProgress := time.Time{}
	n, err := transfer.UploadRawFile(ctx, path, writerFunc(e.write), func(written, total int64) {
		now := time.Now()
		if written != total && now.Sub(lastProgress) < 100*time.Millisecond {
			return
		}
		lastProgress = now
		e.emit(UploadProgress{Path: path, Written: written, Total: total})
	})
	if errors.Is(err, context.Canceled) {
		err = context.Canceled
	}

	e.mu.Lock()
	e.uploading = false
	e.uploadCancel = nil
	e.mu.Unlock()

	e.emit(UploadFinished{Path: path, Bytes: n, Err: err})
	if err != nil && !errors.Is(err, context.Canceled) {
		e.logWarn("transfer.upload_failed", "path", path, "bytes", n, "error", err)
	} else if err == nil {
		e.logInfo("transfer.upload_completed", "path", path, "bytes", n)
	}
}

type writerFunc func(context.Context, []byte) error

func (f writerFunc) Write(data []byte) (int, error) {
	if err := f(context.Background(), data); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (e *endpoint) CancelUpload() {
	e.mu.Lock()
	cancel := e.uploadCancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *endpoint) Quit() { e.cancel() }

func (e *endpoint) emit(event Event) bool {
	select {
	case e.events <- event:
		return true
	case <-e.ctx.Done():
		return false
	}
}

func (e *endpoint) stop() {
	e.mu.Lock()
	e.stopping = true
	cancel := e.uploadCancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *endpoint) logInfo(event string, values ...any) {
	if e.logger != nil {
		e.logger.Info(event, values...)
	}
}

func (e *endpoint) logWarn(event string, values ...any) {
	if e.logger != nil {
		e.logger.Warn(event, values...)
	}
}

func (s *Session) Run(parent context.Context) error {
	if s.cfg.Port == nil {
		return errors.New("serial port is nil")
	}
	if s.cfg.Frontend == nil {
		return errors.New("frontend is nil")
	}

	ctx, cancel := context.WithCancel(parent)
	e := &endpoint{
		ctx: ctx, cancel: cancel, events: make(chan Event, 32),
		writes: make(chan writeRequest), logger: s.cfg.Logger,
	}

	results := make(chan error, 2)
	var backend sync.WaitGroup
	backend.Add(2)
	go func() {
		defer backend.Done()
		results <- s.runWriter(ctx, e.writes)
	}()
	go func() {
		defer backend.Done()
		results <- s.runReader(ctx, e)
	}()

	frontendDone := make(chan error, 1)
	go func() { frontendDone <- s.cfg.Frontend.Run(ctx, e) }()

	var runErr error
	backendResults := 0
	frontendReturned := false
	select {
	case err := <-results:
		runErr = normalizeRunError(err)
		backendResults++
	case err := <-frontendDone:
		runErr = normalizeRunError(err)
		frontendReturned = true
	case <-ctx.Done():
	case <-parent.Done():
	}

	cancel()
	e.stop()
	closeErr := s.cfg.Port.Close()
	backend.Wait()
	e.workers.Wait()
	close(e.events)

	for backendResults < 2 {
		err := <-results
		if runErr == nil {
			runErr = normalizeRunError(err)
		}
		backendResults++
	}
	if !frontendReturned {
		err := <-frontendDone
		if runErr == nil {
			runErr = normalizeRunError(err)
		}
	}
	if closeErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("close serial port: %w", closeErr))
	}
	return runErr
}

func (s *Session) runWriter(ctx context.Context, requests <-chan writeRequest) error {
	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		case req := <-requests:
			err := transfer.WriteFull(s.cfg.Port, req.data)
			req.done <- err
			if err != nil {
				return fmt.Errorf("write serial port: %w", err)
			}
		}
	}
}

func (s *Session) runReader(ctx context.Context, e *endpoint) error {
	var recorder io.Writer
	if s.cfg.ReceiveLog != nil {
		recorder = s.cfg.ReceiveLog
		if s.cfg.ReceiveTimeFormat != "" {
			recorder = newLineTimeWriter(recorder, s.cfg.ReceiveTimeFormat)
		}
	}
	buf := make([]byte, 4096)
	for {
		n, err := s.cfg.Port.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			if recorder != nil {
				if _, writeErr := recorder.Write(data); writeErr != nil {
					return fmt.Errorf("record received data: %w", writeErr)
				}
			}
			if !e.emit(Received{Data: data, At: time.Now()}) {
				return context.Canceled
			}
		}
		if err != nil {
			return fmt.Errorf("read serial port: %w", err)
		}
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
		}
	}
}

func normalizeRunError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
