package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

type SerialPort interface {
	io.ReadWriteCloser
}

type Terminal interface {
	MakeRaw() error
	Restore() error
}

type Logger interface {
	Info(event string, keyValues ...any)
	Warn(event string, keyValues ...any)
}

type OSTerminal struct {
	file     *os.File
	oldState *term.State
}

func NewOSTerminal(file *os.File) *OSTerminal {
	return &OSTerminal{file: file}
}

func (t *OSTerminal) MakeRaw() error {
	oldState, err := term.MakeRaw(int(t.file.Fd()))
	if err != nil {
		return err
	}
	t.oldState = oldState
	return nil
}

func (t *OSTerminal) Restore() error {
	if t.oldState == nil {
		return nil
	}
	return term.Restore(int(t.file.Fd()), t.oldState)
}

type Config struct {
	Port      SerialPort
	Terminal  Terminal
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	PrefixKey byte
	Logger    Logger
}

type Session struct {
	port      SerialPort
	terminal  Terminal
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	prefixKey byte
	logger    Logger
}

func New(cfg Config) *Session {
	prefixKey := cfg.PrefixKey
	if prefixKey == 0 {
		prefixKey = DefaultPrefixKey
	}

	return &Session{
		port:      cfg.Port,
		terminal:  cfg.Terminal,
		stdin:     cfg.Stdin,
		stdout:    cfg.Stdout,
		stderr:    cfg.Stderr,
		prefixKey: prefixKey,
		logger:    cfg.Logger,
	}
}

func (s *Session) Run(ctx context.Context) error {
	if s.port == nil {
		return errors.New("serial port is nil")
	}
	if s.terminal == nil {
		return errors.New("terminal is nil")
	}
	if s.stdin == nil {
		s.stdin = os.Stdin
	}
	if s.stdout == nil {
		s.stdout = os.Stdout
	}
	if s.stderr == nil {
		s.stderr = os.Stderr
	}

	if err := s.terminal.MakeRaw(); err != nil {
		return err
	}
	defer s.terminal.Restore()
	defer s.port.Close()

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 2)
	var closeOnce sync.Once
	closePort := func() {
		closeOnce.Do(func() {
			_ = s.port.Close()
		})
	}

	go func() {
		errCh <- copySerialToStdout(ctx, s.port, s.stdout)
	}()
	go func() {
		errCh <- s.copyStdinToSerial(ctx, cancel)
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			runErr = err
		}
		cancel()
	}
	closePort()

	select {
	case err := <-errCh:
		if runErr == nil && err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
			runErr = err
		}
	default:
	}

	return runErr
}

func copySerialToStdout(ctx context.Context, serial io.Reader, stdout io.Writer) error {
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
		}

		n, err := serial.Read(buf)
		if n > 0 {
			if _, writeErr := stdout.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			return err
		}
	}
}

func (s *Session) copyStdinToSerial(ctx context.Context, cancel context.CancelFunc) error {
	machine := NewPrefixMachine(s.prefixKey)
	buf := make([]byte, 1)

	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
		}

		n, err := s.stdin.Read(buf)
		if n > 0 {
			action, handleErr := machine.HandleByte(buf[0], s.port)
			if handleErr != nil {
				return handleErr
			}

			switch action {
			case ActionHelp:
				printHelp(s.stderr)
			case ActionUpload:
				s.uploadFile(ctx)
			case ActionQuit:
				printLocalLine(s.stderr, "")
				s.logInfo("session.closing", "reason", "user_request")
				cancel()
				return nil
			}
		}
		if err != nil {
			return err
		}
	}
}

func printHelp(w io.Writer) {
	printLocalLine(w, "")
	printLocalLine(w, "[[ xserial ]] local commands:")
	printLocalLine(w, "  Ctrl-A h       show this help")
	printLocalLine(w, "  Ctrl-A u       upload raw file")
	printLocalLine(w, "  Ctrl-A q       quit")
	printLocalLine(w, "  Ctrl-A Ctrl-A  send Ctrl-A")
}

func (s *Session) uploadFile(ctx context.Context) {
	path, err := s.readUploadPath(ctx)
	if err != nil {
		s.logWarn("transfer.upload_canceled", "error", err)
		return
	}
	if path == "" {
		s.logInfo("transfer.upload_canceled", "reason", "empty_path")
		return
	}

	n, err := uploadRawFile(ctx, path, s.port, s.stderr)
	if err != nil {
		s.logWarn("transfer.upload_failed", "path", path, "error", err)
		return
	}

	s.logInfo("transfer.upload_completed", "path", path, "bytes", n)
}

func (s *Session) logInfo(event string, keyValues ...any) {
	if s.logger != nil {
		s.logger.Info(event, keyValues...)
	}
}

func (s *Session) logWarn(event string, keyValues ...any) {
	if s.logger != nil {
		s.logger.Warn(event, keyValues...)
	}
}

func printLocal(w io.Writer, text string) {
	fmt.Fprint(w, text)
}

func printLocalLine(w io.Writer, line string) {
	fmt.Fprintf(w, "%s\r\n", line)
}
