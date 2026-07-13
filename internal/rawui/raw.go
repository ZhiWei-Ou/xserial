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
	inputYMODEMUploadPath
	inputYMODEMTransfer
	inputCancellingTransfer
)

type localAction int

const (
	actionHelp localAction = iota
	actionUpload
	actionYMODEMUpload
	actionYMODEMDownload
	actionQuit
)

type localCommand struct {
	keys        string
	label       string
	description string
	action      localAction
}

var localCommands = []localCommand{
	{keys: "hH?", label: "h", description: "show this help", action: actionHelp},
	{keys: "uU", label: "u", description: "upload raw file", action: actionUpload},
	{keys: "\x15", label: "Ctrl-U", description: "upload file with YMODEM", action: actionYMODEMUpload},
	{keys: "\x04", label: "Ctrl-D", description: "download file with YMODEM", action: actionYMODEMDownload},
	{keys: "qQ", label: "q", description: "quit", action: actionQuit},
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

	requests := make(chan struct{}, 1)
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
	ymodemMode := false
	progressLineOpen := false
	var path strings.Builder
	requestRead := func() {
		select {
		case requests <- struct{}{}:
		case <-ctx.Done():
		default:
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
				if ymodemMode {
					continue
				}
				if err := transfer.WriteFull(f.cfg.Output, event.Data); err != nil {
					return fmt.Errorf("write device output: %w", err)
				}
			case session.UploadProgress:
				progressLineOpen = event.Written != event.Total
				ending := ""
				if !progressLineOpen {
					ending = "\r\n"
				}
				fmt.Fprintf(f.cfg.Local, "\r[ RAW UPLOAD ] %d/%d bytes%s", event.Written, event.Total, ending)
			case session.UploadFinished:
				if progressLineOpen {
					printLocalLine(f.cfg.Local, "")
					progressLineOpen = false
				}
				if errors.Is(event.Err, context.Canceled) {
					printLocalLine(f.cfg.Local, "[ RAW UPLOAD ] canceled")
				} else if event.Err != nil {
					printLocalLine(f.cfg.Local, fmt.Sprintf("[ RAW UPLOAD ] failed: %v", event.Err))
				} else {
					printLocalLine(f.cfg.Local, fmt.Sprintf("[ RAW UPLOAD ] completed: bytes=%d", event.Bytes))
				}
				event.Acknowledge()
				state = inputNormal
				requestRead()
			case session.YMODEMProgress:
				progressLineOpen = event.Written != event.Total
				ending := ""
				if !progressLineOpen {
					ending = "\r\n"
				}
				fmt.Fprintf(f.cfg.Local, "\r[ YMODEM %s ] %d/%d bytes%s", strings.ToUpper(event.Direction), event.Written, event.Total, ending)
			case session.YMODEMFrameRetry:
				if progressLineOpen {
					printLocalLine(f.cfg.Local, "")
					progressLineOpen = false
				}
				event.Acknowledge()
			case session.YMODEMFinished:
				ymodemMode = false
				if progressLineOpen {
					printLocalLine(f.cfg.Local, "")
					progressLineOpen = false
				}
				result := fmt.Sprintf(
					"bytes=%d crc32=%08x failed_frames=%d retried_frames=%d",
					event.Bytes, event.CRC32, event.FailedFrames, event.RetriedFrames,
				)
				prefix := fmt.Sprintf("[ YMODEM %s ]", strings.ToUpper(event.Direction))
				if errors.Is(event.Err, context.Canceled) {
					printLocalLine(f.cfg.Local, fmt.Sprintf("%s canceled: %s", prefix, result))
				} else if event.Err != nil {
					printLocalLine(f.cfg.Local, fmt.Sprintf("%s failed: %v %s", prefix, event.Err, result))
				} else {
					printLocalLine(f.cfg.Local, fmt.Sprintf("%s completed: %s %s", prefix, event.Path, result))
				}
				event.Acknowledge()
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
						ymodemMode = false
						path.Reset()
						state = inputUploadPath
						fmt.Fprint(f.cfg.Local, "\r\n[ RAW UPLOAD ] file (Esc to cancel): ")
					case actionYMODEMUpload:
						ymodemMode = true
						path.Reset()
						state = inputYMODEMUploadPath
						fmt.Fprint(f.cfg.Local, "\r\n[ YMODEM UPLOAD ] file (Esc to cancel): ")
					case actionYMODEMDownload:
						ymodemMode = true
						printLocalLine(f.cfg.Local, "")
						if err := endpoint.StartYMODEMDownload(ctx, "."); err != nil {
							ymodemMode = false
							printLocalLine(f.cfg.Local, fmt.Sprintf("[ YMODEM DOWNLOAD ] failed: %v", err))
						} else {
							state = inputYMODEMTransfer
							printLocalLine(f.cfg.Local, "[ YMODEM DOWNLOAD ] waiting for sender (Esc to cancel)")
						}
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
			case inputUploadPath, inputYMODEMUploadPath:
				switch result.b {
				case 0x1b:
					ymodemMode = false
					path.Reset()
					state = inputNormal
					printLocalLine(f.cfg.Local, "")
					printLocalLine(f.cfg.Local, "[ LOCAL ] file selection canceled")
				case '\r', '\n':
					printLocalLine(f.cfg.Local, "")
					name := strings.TrimSpace(path.String())
					if name == "" {
						ymodemMode = false
						state = inputNormal
					} else if state == inputUploadPath {
						if err := endpoint.StartUpload(ctx, name); err != nil {
							printLocalLine(f.cfg.Local, fmt.Sprintf("[ RAW UPLOAD ] failed: %v", err))
							state = inputNormal
						} else {
							state = inputUploading
							printLocalLine(f.cfg.Local, "[ RAW UPLOAD ] started (Esc to cancel)")
						}
					} else if err := endpoint.StartYMODEMUpload(ctx, name); err != nil {
						ymodemMode = false
						printLocalLine(f.cfg.Local, fmt.Sprintf("[ YMODEM UPLOAD ] failed: %v", err))
						state = inputNormal
					} else {
						state = inputYMODEMTransfer
						printLocalLine(f.cfg.Local, "[ YMODEM UPLOAD ] started (Esc to cancel)")
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
			case inputUploading, inputYMODEMTransfer:
				if result.b == 0x1b {
					endpoint.CancelTransfer()
					state = inputCancellingTransfer
					printLocalLine(f.cfg.Local, "\r[ TRANSFER ] canceling")
				}
			case inputCancellingTransfer:
			}
			if state != inputCancellingTransfer {
				requestRead()
			}
		}
	}
}

func printHelp(w io.Writer) {
	printLocalLine(w, "")
	printLocalLine(w, "[ LOCAL ] commands:")
	for _, command := range localCommands {
		printLocalLine(w, fmt.Sprintf("  Ctrl-P %-7s %s", command.label, command.description))
	}
	printLocalLine(w, "  Ctrl-P Ctrl-P  send Ctrl-P")
	printLocalLine(w, "  Esc            cancel file selection or transfer")
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
