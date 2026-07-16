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

var (
	ErrDisconnected   = errors.New("serial port is disconnected")
	ErrTransferActive = errors.New("transfer is active")
)

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

type Disconnected struct {
	Err error
}

func (Disconnected) isSessionEvent() {}

type Reconnected struct{}

func (Reconnected) isSessionEvent() {}

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

	acknowledged chan struct{}
}

func (UploadFinished) isSessionEvent() {}

// Acknowledge lets the session log a transfer result only after the frontend
// has closed its in-place progress line and rendered the result.
func (e UploadFinished) Acknowledge() {
	select {
	case e.acknowledged <- struct{}{}:
	default:
	}
}

type YMODEMProgress struct {
	Direction      string
	Path           string
	Written, Total int64
}

func (YMODEMProgress) isSessionEvent() {}

type YMODEMFrameRetry struct {
	Direction string
	Path      string
	Block     byte
	Attempt   int
	Reason    string

	acknowledged chan struct{}
}

func (YMODEMFrameRetry) isSessionEvent() {}

func (e YMODEMFrameRetry) Acknowledge() {
	select {
	case e.acknowledged <- struct{}{}:
	default:
	}
}

type YMODEMFinished struct {
	Direction     string
	Path          string
	Bytes         int64
	CRC32         uint32
	FailedFrames  int
	RetriedFrames int
	Err           error

	acknowledged chan struct{}
}

func (YMODEMFinished) isSessionEvent() {}

// Acknowledge lets the session log a transfer result only after the frontend
// has closed its in-place progress line and rendered the result.
func (e YMODEMFinished) Acknowledge() {
	select {
	case e.acknowledged <- struct{}{}:
	default:
	}
}

type Endpoint interface {
	Events() <-chan Event
	Send(context.Context, []byte) error
	StartUpload(context.Context, string) error
	StartYMODEMUpload(context.Context, string) error
	StartYMODEMDownload(context.Context, string) error
	CancelTransfer()
	Quit()
}

type Frontend interface {
	Run(context.Context, Endpoint) error
}

type Config struct {
	Port              SerialPort
	Reconnect         func() (SerialPort, error)
	ReconnectInterval time.Duration
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

	mu                    sync.Mutex
	stopping              bool
	transferActive        bool
	transferConsumesInput bool
	transferCancel        context.CancelFunc
	transferInput         chan []byte
	workers               sync.WaitGroup
	portMu                sync.RWMutex
	port                  SerialPort
	portGeneration        uint64
}

func (e *endpoint) Events() <-chan Event { return e.events }

func (e *endpoint) Send(ctx context.Context, data []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return context.Canceled
	}
	if e.transferActive {
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
	return e.startTransfer(ctx, false, func(transferCtx context.Context) {
		e.runUpload(transferCtx, path)
	})
}

func (e *endpoint) StartYMODEMUpload(ctx context.Context, path string) error {
	return e.startTransfer(ctx, true, func(transferCtx context.Context) {
		e.runYMODEMUpload(transferCtx, path)
	})
}

func (e *endpoint) StartYMODEMDownload(ctx context.Context, dir string) error {
	return e.startTransfer(ctx, true, func(transferCtx context.Context) {
		e.runYMODEMDownload(transferCtx, dir)
	})
}

func (e *endpoint) startTransfer(ctx context.Context, consumesInput bool, run func(context.Context)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		return context.Canceled
	}
	if e.transferActive {
		e.mu.Unlock()
		return ErrTransferActive
	}
	transferCtx, cancel := context.WithCancel(e.ctx)
	e.transferActive = true
	e.transferConsumesInput = consumesInput
	e.transferCancel = cancel
	if consumesInput {
		e.transferInput = make(chan []byte, 32)
	}
	e.workers.Add(1)
	e.mu.Unlock()

	go func() {
		defer e.workers.Done()
		run(transferCtx)
	}()
	return nil
}

func (e *endpoint) runUpload(ctx context.Context, path string) {
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

	e.finishTransfer()

	finished := UploadFinished{Path: path, Bytes: n, Err: err, acknowledged: make(chan struct{}, 1)}
	if e.emit(finished) {
		e.waitForAcknowledgement(ctx, finished.acknowledged)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		e.logWarn("transfer.upload_failed", "path", path, "bytes", n, "error", err)
	} else if err == nil {
		e.logInfo("transfer.upload_completed", "path", path, "bytes", n)
	}
}

func (e *endpoint) runYMODEMUpload(ctx context.Context, path string) {
	stream := &transferStream{ctx: ctx, endpoint: e, input: e.transferInput}
	stats, err := transfer.SendYMODEMFile(ctx, path, stream, func(written, total int64) {
		e.emit(YMODEMProgress{Direction: "upload", Path: path, Written: written, Total: total})
	}, func(retry transfer.YMODEMRetry) {
		e.reportYMODEMRetry(ctx, "upload", path, retry)
	})
	e.finishTransfer()
	finished := newYMODEMFinished("upload", path, stats, err)
	if e.emit(finished) {
		e.waitForAcknowledgement(ctx, finished.acknowledged)
	}
	e.logYMODEMResult("upload", path, stats, err)
}

func (e *endpoint) runYMODEMDownload(ctx context.Context, dir string) {
	stream := &transferStream{ctx: ctx, endpoint: e, input: e.transferInput}
	path, stats, err := transfer.ReceiveYMODEMFile(ctx, dir, stream, func(written, total int64) {
		e.emit(YMODEMProgress{Direction: "download", Written: written, Total: total})
	}, func(retry transfer.YMODEMRetry) {
		e.reportYMODEMRetry(ctx, "download", retry.Path, retry)
	})
	e.finishTransfer()
	finished := newYMODEMFinished("download", path, stats, err)
	if e.emit(finished) {
		e.waitForAcknowledgement(ctx, finished.acknowledged)
	}
	e.logYMODEMResult("download", path, stats, err)
}

func newYMODEMFinished(direction, path string, stats transfer.YMODEMStats, err error) YMODEMFinished {
	return YMODEMFinished{
		Direction: direction, Path: path, Bytes: stats.Bytes, CRC32: stats.CRC32,
		FailedFrames: stats.FailedFrames, RetriedFrames: stats.RetriedFrames,
		Err: err, acknowledged: make(chan struct{}, 1),
	}
}

func (e *endpoint) reportYMODEMRetry(ctx context.Context, direction, path string, retry transfer.YMODEMRetry) {
	event := YMODEMFrameRetry{
		Direction: direction, Path: path, Block: retry.Block,
		Attempt: retry.Attempt, Reason: retry.Reason,
		acknowledged: make(chan struct{}, 1),
	}
	if e.emit(event) {
		e.waitForAcknowledgement(ctx, event.acknowledged)
	}
	e.logWarn(
		"transfer.ymodem_frame_retry",
		"direction", direction,
		"path", path,
		"block", retry.Block,
		"attempt", retry.Attempt,
		"reason", retry.Reason,
	)
}

func (e *endpoint) logYMODEMResult(direction, path string, stats transfer.YMODEMStats, err error) {
	values := []any{
		"direction", direction,
		"path", path,
		"bytes", stats.Bytes,
		"crc32", fmt.Sprintf("%08x", stats.CRC32),
		"failed_frames", stats.FailedFrames,
		"retried_frames", stats.RetriedFrames,
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		e.logWarn("transfer.ymodem_failed", append(values, "error", err)...)
	} else if err == nil {
		e.logInfo("transfer.ymodem_completed", values...)
	}
}

func (e *endpoint) waitForAcknowledgement(ctx context.Context, acknowledged <-chan struct{}) {
	select {
	case <-acknowledged:
	case <-ctx.Done():
	case <-e.ctx.Done():
	}
}

func (e *endpoint) finishTransfer() {
	e.mu.Lock()
	e.transferActive = false
	e.transferConsumesInput = false
	e.transferCancel = nil
	e.transferInput = nil
	e.mu.Unlock()
}

type writerFunc func(context.Context, []byte) error

func (f writerFunc) Write(data []byte) (int, error) {
	if err := f(context.Background(), data); err != nil {
		return 0, err
	}
	return len(data), nil
}

type transferStream struct {
	ctx      context.Context
	endpoint *endpoint
	input    <-chan []byte
	pending  []byte
}

func (s *transferStream) Read(data []byte) (int, error) {
	for len(s.pending) == 0 {
		select {
		case chunk := <-s.input:
			s.pending = chunk
		case <-s.ctx.Done():
			return 0, context.Canceled
		case <-s.endpoint.ctx.Done():
			return 0, context.Canceled
		}
	}
	n := copy(data, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *transferStream) Write(data []byte) (int, error) {
	if err := s.endpoint.write(s.ctx, data); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (e *endpoint) CancelTransfer() {
	e.mu.Lock()
	cancel := e.transferCancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *endpoint) Quit() { e.cancel() }

func (e *endpoint) connection() (SerialPort, uint64) {
	e.portMu.RLock()
	defer e.portMu.RUnlock()
	return e.port, e.portGeneration
}

func (e *endpoint) setConnection(port SerialPort, generation uint64) {
	e.portMu.Lock()
	e.port = port
	e.portGeneration = generation
	e.portMu.Unlock()
}

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
	cancel := e.transferCancel
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
	e.setConnection(s.cfg.Port, 1)

	failures := make(chan connectionFailure, 2)
	writerDone := make(chan error, 1)
	go func() { writerDone <- s.runWriter(ctx, e, failures) }()
	connectionDone := make(chan error, 1)
	go func() { connectionDone <- s.runConnections(ctx, e, failures) }()

	frontendDone := make(chan error, 1)
	go func() { frontendDone <- s.cfg.Frontend.Run(ctx, e) }()

	var runErr error
	connectionReturned := false
	frontendReturned := false
	select {
	case err := <-connectionDone:
		runErr = normalizeRunError(err)
		connectionReturned = true
	case err := <-frontendDone:
		runErr = normalizeRunError(err)
		frontendReturned = true
	case <-ctx.Done():
	case <-parent.Done():
	}

	cancel()
	e.stop()
	if !connectionReturned {
		if err := <-connectionDone; runErr == nil {
			runErr = normalizeRunError(err)
		}
	}
	if err := <-writerDone; runErr == nil {
		runErr = normalizeRunError(err)
	}
	e.workers.Wait()
	close(e.events)

	if !frontendReturned {
		err := <-frontendDone
		if runErr == nil {
			runErr = normalizeRunError(err)
		}
	}
	return runErr
}

func (s *Session) runWriter(ctx context.Context, e *endpoint, failures chan<- connectionFailure) error {
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
			err := transfer.WriteFull(port, req.data)
			if err != nil {
				req.done <- fmt.Errorf("%w: %v", ErrDisconnected, err)
				select {
				case failures <- connectionFailure{generation: generation, err: fmt.Errorf("write serial port: %w", err)}:
				case <-ctx.Done():
					return context.Canceled
				}
				continue
			}
			req.done <- nil
		}
	}
}

func (s *Session) runReader(ctx context.Context, e *endpoint, port SerialPort) error {
	var recorder io.Writer
	if s.cfg.ReceiveLog != nil {
		recorder = s.cfg.ReceiveLog
		if s.cfg.ReceiveTimeFormat != "" {
			recorder = newLineTimeWriter(recorder, s.cfg.ReceiveTimeFormat)
		}
	}
	buf := make([]byte, 4096)
	for {
		n, err := port.Read(buf)
		if n > 0 {
			data := append([]byte(nil), buf[:n]...)
			if recorder != nil {
				if _, writeErr := recorder.Write(data); writeErr != nil {
					return fmt.Errorf("record received data: %w", writeErr)
				}
			}
			if !e.deliverReceived(ctx, data) {
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

func (e *endpoint) deliverReceived(ctx context.Context, data []byte) bool {
	e.mu.Lock()
	consumesInput := e.transferConsumesInput
	input := e.transferInput
	e.mu.Unlock()
	if consumesInput {
		select {
		case input <- data:
			return true
		case <-ctx.Done():
			return false
		}
	}
	return e.emit(Received{Data: data, At: time.Now()})
}

func normalizeRunError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
