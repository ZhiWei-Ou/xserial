package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/backend"
	"github.com/ZhiWei-Ou/xserial/internal/linetime"
	"github.com/ZhiWei-Ou/xserial/internal/logging"
	"github.com/ZhiWei-Ou/xserial/internal/transfer"
)

var (
	ErrDisconnected    = errors.New("serial port is disconnected")
	ErrTransferActive  = errors.New("transfer is active")
	ErrNotConfigurable = errors.New("serial session is not configurable")
)

type ConnectionConfig struct {
	PortName string
	BaudRate int
	DataBits int
	Parity   string
	StopBits string
}

type SerialPort interface {
	io.ReadWriteCloser
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

type Reconnecting struct {
	Attempt int
	Limit   int
	Err     error
}

func (Reconnecting) isSessionEvent() {}

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
}

func (YMODEMFinished) isSessionEvent() {}

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
	ReconnectAttempts int
	OpenConnection    func(ConnectionConfig) (SerialPort, error)
	Frontend          Frontend
	ReceiveLog        io.Writer
	ReceiveTimeFormat string
	Logger            *logging.Logger
	Handlers          []Handler
}

type Session struct {
	cfg Config
}

func New(cfg Config) *Session { return &Session{cfg: cfg} }

type endpoint struct {
	ctx            context.Context
	cancel         context.CancelFunc
	events         chan Event
	ready          chan struct{}
	readyOnce      sync.Once
	logger         *logging.Logger
	backend        *backend.Endpoint
	pipeline       *Pipeline
	openConnection func(ConnectionConfig) (SerialPort, error)

	mu                    sync.Mutex
	stopping              bool
	transferActive        bool
	transferConsumesInput bool
	transferCancel        context.CancelFunc
	transferInput         chan []byte
	transferHandlerName   string
	workers               sync.WaitGroup
}

func (e *endpoint) Events() <-chan Event {
	e.markReady()
	return e.events
}

func (e *endpoint) markReady() { e.readyOnce.Do(func() { close(e.ready) }) }

type transferGate struct {
	name   string
	duplex bool
	input  chan<- []byte
	done   <-chan struct{}
}

func (g *transferGate) Name() string { return g.name }
func (g *transferGate) Capability() Capability {
	if g.duplex {
		return ExclusiveDuplex
	}
	return ExclusiveOutbound
}
func (g *transferGate) HandleInbound(ctx context.Context, envelope Envelope) (Action, error) {
	if !g.duplex {
		return Forward(envelope), nil
	}
	select {
	case g.input <- append([]byte(nil), envelope.Data...):
		return Consume(), nil
	case <-g.done:
		return Consume(), nil
	case <-ctx.Done():
		return Action{}, ctx.Err()
	}
}

func (e *endpoint) Send(ctx context.Context, data []byte) error {
	e.markReady()
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

func (e *endpoint) Configure(ctx context.Context, cfg ConnectionConfig) error {
	e.markReady()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return context.Canceled
	}
	if e.transferActive {
		return ErrTransferActive
	}
	if e.openConnection == nil {
		return ErrNotConfigurable
	}
	return e.backend.Reconfigure(ctx, func() (backend.Port, error) {
		return e.openConnection(cfg)
	})
}

func (e *endpoint) write(ctx context.Context, data []byte) error {
	return e.writeFrom(ctx, "frontend", data)
}

func (e *endpoint) writeTransfer(ctx context.Context, data []byte) error {
	return e.writeFrom(ctx, "transfer.active", data)
}

func (e *endpoint) writeFrom(ctx context.Context, source string, data []byte) error {
	envelope, err := e.pipeline.ProcessOutbound(ctx, Envelope{
		Data: data, Source: source, At: time.Now(),
	})
	if err != nil {
		if errors.Is(err, ErrDirectionBusy) {
			return ErrTransferActive
		}
		return err
	}
	if err := e.backend.Send(ctx, envelope.Data); errors.Is(err, backend.ErrDisconnected) {
		return fmt.Errorf("%w: %w", ErrDisconnected, err)
	} else {
		return err
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
	gate := &transferGate{name: "transfer.active", duplex: consumesInput, input: e.transferInput, done: transferCtx.Done()}
	if err := e.pipeline.Add(ctx, gate); err != nil {
		cancel()
		e.transferActive = false
		e.transferConsumesInput = false
		e.transferCancel = nil
		e.transferInput = nil
		e.mu.Unlock()
		return err
	}
	e.transferHandlerName = gate.name
	e.workers.Add(1)
	e.mu.Unlock()
	e.markReady()

	go func() {
		defer e.workers.Done()
		run(transferCtx)
	}()
	return nil
}

func (e *endpoint) runUpload(ctx context.Context, path string) {
	e.emit(UploadStarted{Path: path})

	lastProgress := time.Time{}
	n, err := transfer.UploadRawFile(ctx, path, writerFunc(e.writeTransfer), func(written, total int64) {
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

	e.emit(UploadFinished{Path: path, Bytes: n, Err: err})
}

func (e *endpoint) runYMODEMUpload(ctx context.Context, path string) {
	stream := &transferStream{ctx: ctx, endpoint: e, input: e.transferInput}
	stats, err := transfer.SendYMODEMFile(ctx, path, stream, func(written, total int64) {
		e.emit(YMODEMProgress{Direction: "upload", Path: path, Written: written, Total: total})
	}, func(retry transfer.YMODEMRetry) {
		e.reportYMODEMRetry(ctx, "upload", path, retry)
	})
	e.finishTransfer()
	e.emit(newYMODEMFinished("upload", path, stats, err))
}

func (e *endpoint) runYMODEMDownload(ctx context.Context, dir string) {
	stream := &transferStream{ctx: ctx, endpoint: e, input: e.transferInput}
	path, stats, err := transfer.ReceiveYMODEMFile(ctx, dir, stream, func(written, total int64) {
		e.emit(YMODEMProgress{Direction: "download", Written: written, Total: total})
	}, func(retry transfer.YMODEMRetry) {
		e.reportYMODEMRetry(ctx, "download", retry.Path, retry)
	})
	e.finishTransfer()
	e.emit(newYMODEMFinished("download", path, stats, err))
}

func newYMODEMFinished(direction, path string, stats transfer.YMODEMStats, err error) YMODEMFinished {
	return YMODEMFinished{
		Direction: direction, Path: path, Bytes: stats.Bytes, CRC32: stats.CRC32,
		FailedFrames: stats.FailedFrames, RetriedFrames: stats.RetriedFrames,
		Err: err,
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
	e.logger.Warn(
		"transfer.ymodem_frame_retry",
		"direction", direction,
		"path", path,
		"block", retry.Block,
		"attempt", retry.Attempt,
		"reason", retry.Reason,
	)
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
	handlerName := e.transferHandlerName
	e.transferActive = false
	e.transferConsumesInput = false
	e.transferCancel = nil
	e.transferInput = nil
	e.transferHandlerName = ""
	e.mu.Unlock()
	if handlerName != "" {
		_ = e.pipeline.Remove(context.Background(), handlerName)
	}
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
	if err := s.endpoint.writeTransfer(s.ctx, data); err != nil {
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

func (e *endpoint) Quit() {
	e.markReady()
	e.cancel()
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

func (s *Session) Run(parent context.Context) error {
	if s.cfg.Port == nil {
		return errors.New("serial port is nil")
	}
	if s.cfg.Frontend == nil {
		return errors.New("frontend is nil")
	}

	ctx, cancel := context.WithCancel(parent)
	pipeline, err := NewPipeline(s.cfg.Handlers...)
	if err != nil {
		cancel()
		return err
	}
	attempts := s.cfg.ReconnectAttempts
	if attempts == 0 && s.cfg.Reconnect != nil {
		attempts = backend.DefaultReconnectAttempts
	}
	if attempts < 0 {
		attempts = 0
	}
	backendSession := backend.New(backend.Config{
		Port: s.cfg.Port,
		Reconnect: func() (backend.Port, error) {
			if s.cfg.Reconnect == nil {
				return nil, ErrDisconnected
			}
			return s.cfg.Reconnect()
		},
		ReconnectAttempts: attempts,
		ReconnectInterval: s.cfg.ReconnectInterval,
	})
	ready := make(chan *backend.Endpoint, 1)
	backendDone := make(chan error, 1)
	go func() { backendDone <- backendSession.Run(ctx, ready) }()
	var backendEndpoint *backend.Endpoint
	select {
	case backendEndpoint = <-ready:
	case err := <-backendDone:
		cancel()
		return normalizeRunError(err)
	case <-parent.Done():
		cancel()
		return nil
	}
	e := &endpoint{
		ctx: ctx, cancel: cancel, events: make(chan Event, 32), logger: s.cfg.Logger,
		ready: make(chan struct{}), backend: backendEndpoint, pipeline: pipeline,
		openConnection: s.cfg.OpenConnection,
	}
	dispatcherDone := make(chan error, 1)
	go func() { dispatcherDone <- s.runBackendEvents(ctx, e) }()

	frontendDone := make(chan error, 1)
	go func() { frontendDone <- s.cfg.Frontend.Run(ctx, e) }()

	var runErr error
	backendReturned := false
	dispatcherReturned := false
	frontendReturned := false
	select {
	case err := <-backendDone:
		runErr = normalizeRunError(err)
		backendReturned = true
	case err := <-dispatcherDone:
		runErr = normalizeRunError(err)
		dispatcherReturned = true
	case err := <-frontendDone:
		runErr = normalizeRunError(err)
		frontendReturned = true
	case <-ctx.Done():
	case <-parent.Done():
	}

	if backendReturned && !dispatcherReturned {
		if err := <-dispatcherDone; runErr == nil {
			runErr = normalizeRunError(err)
		}
		dispatcherReturned = true
	}
	cancel()
	e.stop()
	if !backendReturned {
		if err := <-backendDone; runErr == nil {
			runErr = normalizeRunError(err)
		}
	}
	if !dispatcherReturned {
		if err := <-dispatcherDone; runErr == nil {
			runErr = normalizeRunError(err)
		}
	}
	e.workers.Wait()
	if err := pipeline.Close(context.Background()); runErr == nil {
		runErr = err
	}
	close(e.events)

	if !frontendReturned {
		err := <-frontendDone
		if runErr == nil {
			runErr = normalizeRunError(err)
		}
	}
	return runErr
}

func (s *Session) runBackendEvents(ctx context.Context, e *endpoint) error {
	select {
	case <-e.ready:
	case <-ctx.Done():
		return context.Canceled
	}
	var recorder io.Writer
	if s.cfg.ReceiveLog != nil {
		recorder = s.cfg.ReceiveLog
		if s.cfg.ReceiveTimeFormat != "" {
			recorder = linetime.NewWriter(recorder, s.cfg.ReceiveTimeFormat)
		}
	}
	for event := range e.backend.Events() {
		switch event := event.(type) {
		case backend.Received:
			if recorder != nil {
				if _, err := recorder.Write(event.Data); err != nil {
					return fmt.Errorf("record received data: %w", err)
				}
			}
			if !e.deliverReceived(ctx, event.Data, event.At) {
				return context.Canceled
			}
		case backend.Disconnected:
			e.CancelTransfer()
			e.logger.Warn("session.disconnected", "error", event.Err)
			e.emit(Disconnected{Err: event.Err})
		case backend.Reconnecting:
			if event.Attempt > 1 {
				e.logger.Warn("session.reconnect_failed", "attempt", event.Attempt-1, "error", event.Err)
			}
			e.logger.Warn("session.reconnecting", "attempt", event.Attempt, "limit", event.Limit)
			e.emit(Reconnecting{Attempt: event.Attempt, Limit: event.Limit, Err: event.Err})
		case backend.Reconnected:
			e.logger.Info("session.reconnected", "attempt", event.Attempt)
			e.emit(Reconnected{})
		}
	}
	return nil
}

func (e *endpoint) deliverReceived(ctx context.Context, data []byte, at time.Time) bool {
	envelope, err := e.pipeline.ProcessInbound(ctx, Envelope{Data: data, Source: "serial", At: at})
	if errors.Is(err, ErrConsumed) {
		return true
	}
	if err != nil {
		return false
	}
	return e.emit(Received{Data: envelope.Data, At: envelope.At})
}

func normalizeRunError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
