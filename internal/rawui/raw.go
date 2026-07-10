package rawui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/ZhiWei-Ou/xserial/internal/session"
	"github.com/ZhiWei-Ou/xserial/internal/transfer"
	"github.com/muesli/cancelreader"
	"golang.org/x/term"
)

const DefaultPrefixKey byte = 0x10

type Terminal interface {
	MakeRaw() error
	Restore() error
}

type OSTerminal struct {
	file     *os.File
	oldState *term.State
	mu       sync.Mutex
}

func NewOSTerminal(file *os.File) *OSTerminal { return &OSTerminal{file: file} }

func (t *OSTerminal) MakeRaw() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	oldState, err := term.MakeRaw(int(t.file.Fd()))
	if err != nil {
		return err
	}
	t.oldState = oldState
	return nil
}

func (t *OSTerminal) Restore() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.oldState == nil {
		return nil
	}
	err := term.Restore(int(t.file.Fd()), t.oldState)
	t.oldState = nil
	return err
}

type Config struct {
	Terminal  Terminal
	Input     io.Reader
	Output    io.Writer
	Local     io.Writer
	PrefixKey byte
}

type Frontend struct {
	cfg Config
}

func New(cfg Config) *Frontend {
	if cfg.PrefixKey == 0 {
		cfg.PrefixKey = DefaultPrefixKey
	}
	return &Frontend{cfg: cfg}
}

type inputResult struct {
	b   byte
	err error
}

type inputState int

const (
	inputNormal inputState = iota
	inputAfterPrefix
	inputUploadPath
	inputUploading
)

type localAction int

const (
	actionHelp localAction = iota
	actionUpload
	actionQuit
)

type localCommand struct {
	keys        string
	description string
	action      localAction
}

var localCommands = []localCommand{
	{keys: "hH?", description: "show this help", action: actionHelp},
	{keys: "uU", description: "upload raw file", action: actionUpload},
	{keys: "qQ", description: "quit", action: actionQuit},
}

func (f *Frontend) Run(ctx context.Context, endpoint session.Endpoint) (runErr error) {
	if f.cfg.Terminal == nil {
		return errors.New("terminal is nil")
	}
	if f.cfg.Input == nil || f.cfg.Output == nil || f.cfg.Local == nil {
		return errors.New("raw frontend input/output is nil")
	}
	if err := f.cfg.Terminal.MakeRaw(); err != nil {
		return fmt.Errorf("enter raw mode: %w", err)
	}
	defer func() {
		if err := f.cfg.Terminal.Restore(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("restore terminal: %w", err))
		}
	}()

	reader, err := cancelreader.NewReader(f.cfg.Input)
	if err != nil {
		return fmt.Errorf("create cancelable input: %w", err)
	}
	defer reader.Close()
	go func() {
		<-ctx.Done()
		reader.Cancel()
	}()

	requests := make(chan struct{})
	input := make(chan inputResult, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := []byte{0}
		for range requests {
			n, readErr := reader.Read(buf)
			result := inputResult{err: readErr}
			if n > 0 {
				result.b = buf[0]
			}
			select {
			case input <- result:
			case <-ctx.Done():
				return
			}
			if readErr != nil {
				return
			}
		}
	}()
	defer func() {
		reader.Cancel()
		close(requests)
		<-readerDone
	}()

	state := inputNormal
	var path strings.Builder
	requestRead := func() {
		select {
		case requests <- struct{}{}:
		case <-ctx.Done():
		}
	}
	requestRead()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-endpoint.Events():
			if !ok {
				return nil
			}
			switch event := event.(type) {
			case session.Received:
				if err := transfer.WriteFull(f.cfg.Output, event.Data); err != nil {
					return fmt.Errorf("write device output: %w", err)
				}
			case session.UploadProgress:
				fmt.Fprintf(f.cfg.Local, "\r[[ xserial | TRANSFER ]] %d/%d bytes", event.Written, event.Total)
			case session.UploadFinished:
				printLocalLine(f.cfg.Local, "")
				if event.Err != nil {
					printLocalLine(f.cfg.Local, fmt.Sprintf("[[ xserial ]] upload failed: %v", event.Err))
				} else {
					printLocalLine(f.cfg.Local, fmt.Sprintf("[[ xserial ]] uploaded %d bytes", event.Bytes))
				}
				state = inputNormal
				requestRead()
			}
		case result := <-input:
			if result.err != nil {
				if errors.Is(result.err, cancelreader.ErrCanceled) || ctx.Err() != nil {
					return nil
				}
				endpoint.Quit()
				if errors.Is(result.err, io.EOF) {
					return nil
				}
				return result.err
			}

			switch state {
			case inputNormal:
				if result.b == f.cfg.PrefixKey {
					state = inputAfterPrefix
				} else if err := endpoint.Send(ctx, []byte{result.b}); err != nil {
					return err
				}
			case inputAfterPrefix:
				state = inputNormal
				if result.b == f.cfg.PrefixKey {
					if err := endpoint.Send(ctx, []byte{f.cfg.PrefixKey}); err != nil {
						return err
					}
				} else if command, ok := findLocalCommand(result.b); ok {
					switch command.action {
					case actionHelp:
						printHelp(f.cfg.Local)
					case actionUpload:
						path.Reset()
						state = inputUploadPath
						fmt.Fprint(f.cfg.Local, "\r\n[[ xserial ]] upload file: ")
					case actionQuit:
						printLocalLine(f.cfg.Local, "")
						endpoint.Quit()
						return nil
					}
				} else {
					if err := endpoint.Send(ctx, []byte{f.cfg.PrefixKey, result.b}); err != nil {
						return err
					}
				}
			case inputUploadPath:
				switch result.b {
				case '\r', '\n':
					printLocalLine(f.cfg.Local, "")
					name := strings.TrimSpace(path.String())
					if name == "" {
						state = inputNormal
					} else if err := endpoint.StartUpload(ctx, name); err != nil {
						printLocalLine(f.cfg.Local, fmt.Sprintf("[[ xserial ]] upload failed: %v", err))
						state = inputNormal
					} else {
						state = inputUploading
					}
				case 0x7f, '\b':
					if path.Len() > 0 {
						value := path.String()
						path.Reset()
						path.WriteString(value[:len(value)-1])
						fmt.Fprint(f.cfg.Local, "\b \b")
					}
				default:
					path.WriteByte(result.b)
					_, _ = f.cfg.Local.Write([]byte{result.b})
				}
			}
			if state != inputUploading {
				requestRead()
			}
		}
	}
}

func printHelp(w io.Writer) {
	printLocalLine(w, "")
	printLocalLine(w, "[[ xserial ]] local commands:")
	for _, command := range localCommands {
		printLocalLine(w, fmt.Sprintf("  Ctrl-P %-7c %s", command.keys[0], command.description))
	}
	printLocalLine(w, "  Ctrl-P Ctrl-P  send Ctrl-P")
}

func findLocalCommand(key byte) (localCommand, bool) {
	for _, command := range localCommands {
		if strings.IndexByte(command.keys, key) >= 0 {
			return command, true
		}
	}
	return localCommand{}, false
}

func printLocalLine(w io.Writer, line string) { fmt.Fprintf(w, "%s\r\n", line) }

var _ session.Frontend = (*Frontend)(nil)
