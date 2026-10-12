package rawui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/linetime"
	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
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
	Connection session.ConnectionConfig
	Terminal   Terminal
	Input      io.Reader
	Output     io.Writer
	Local      io.Writer
	PrefixKey  byte
	TimeFormat string
	Hexdump    bool
	StatusBar  *StatusBar
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
	inputYMODEMUploadPath
	inputYMODEMTransfer
	inputCancellingTransfer
)

type localAction int

const (
	actionHelp localAction = iota
	actionInfo
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
	{keys: "iI", label: "i", description: "show connection configuration", action: actionInfo},
	{keys: "hH?", label: "h", description: "show this help", action: actionHelp},
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

	bar := f.cfg.StatusBar
	defer func() { runErr = errors.Join(runErr, bar.close()) }()
	if err := bar.start(f.cfg.Connection); err != nil {
		return fmt.Errorf("start status bar: %w", err)
	}
	var refresh <-chan time.Time
	if bar != nil && bar.size != nil {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		refresh = ticker.C
	}

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
	var progress transferProgress
	defer progress.endLine(f.cfg.Local)
	var path pathPrompt
	var lastUploadPath string
	escapeTimer := time.NewTimer(time.Hour)
	escapeTimer.Stop()
	defer escapeTimer.Stop()
	cancelSelection := func() {
		ymodemMode = false
		state = inputNormal
		bar.update("", nil, time.Time{})
		fmt.Fprint(f.cfg.Local, "\x1b[?2004l\r\n")
		printLocalLine(f.cfg.Local, localColor("File selection canceled", localWarningColor))
	}
	defer func() {
		if state == inputYMODEMUploadPath {
			fmt.Fprint(f.cfg.Local, "\x1b[?2004l")
		}
	}()
	requestRead := func() {
		select {
		case requests <- struct{}{}:
		case <-ctx.Done():
		default:
		}
	}
	requestRead()
	deviceOutput := f.cfg.Output
	if f.cfg.TimeFormat != "" {
		deviceOutput = linetime.NewWriter(deviceOutput, f.cfg.TimeFormat)
	}

	if f.cfg.Hexdump {
		deviceOutput = &hexWriter{dst: deviceOutput}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-refresh:
			resized, err := bar.refresh()
			if err != nil {
				return fmt.Errorf("refresh status bar: %w", err)
			}
			if resized && state == inputYMODEMUploadPath {
				path.width = bar.width
				path.render(f.cfg.Local)
			}
		case <-escapeTimer.C:
			if state == inputYMODEMUploadPath && len(path.pending) > 0 && path.pending[0] == 0x1b {
				if len(path.pending) == 1 && !path.paste {
					cancelSelection()
				}
				path.pending = nil
			}
		case event, ok := <-endpoint.Events():
			if !ok {
				return nil
			}
			switch event := event.(type) {
			case session.Disconnected:
				bar.update("Disconnected", nil, time.Time{})
			case session.Reconnecting:
				bar.update("Reconnecting", nil, time.Time{})
			case session.Reconnected:
				bar.update("Connected", nil, time.Time{})
			case session.Received:
				if ymodemMode {
					continue
				}
				if err := transfer.WriteFull(deviceOutput, event.Data); err != nil {
					return fmt.Errorf("write device output: %w", err)
				}
			case session.YMODEMProgress:
				if bar.enabled() {
					bar.update("", &event, progress.started)
				} else {
					progress.render(f.cfg.Local, event)
				}
			case session.YMODEMFrameRetry:
				progress.endLine(f.cfg.Local)
				event.Acknowledge()
			case session.YMODEMFinished:
				ymodemMode = false
				bar.update("", nil, time.Time{})
				progress.finish(f.cfg.Local, event)
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
				} else if err := endpoint.Send(ctx, []byte{result.b}); err != nil && !errors.Is(err, session.ErrDisconnected) {
					return err
				}
			case inputAfterPrefix:
				state = inputNormal
				if result.b == f.cfg.PrefixKey {
					if err := endpoint.Send(ctx, []byte{f.cfg.PrefixKey}); err != nil && !errors.Is(err, session.ErrDisconnected) {
						return err
					}
				} else if command, ok := findLocalCommand(result.b); ok {
					switch command.action {
					case actionInfo:
						cfg := f.cfg.Connection
						printLocalLine(f.cfg.Local, "")
						printLocalLine(f.cfg.Local, fmt.Sprintf("Port: %s  Baud: %d  Data bits: %d  Parity: %s  Stop bits: %s", cfg.PortName, cfg.BaudRate, cfg.DataBits, cfg.Parity, cfg.StopBits))
					case actionHelp:
						printHelp(f.cfg.Local)
					case actionYMODEMUpload:
						ymodemMode = true
						path = pathPrompt{line: []rune(lastUploadPath), cursor: len([]rune(lastUploadPath))}
						if file, ok := f.cfg.Input.(*os.File); ok {
							path.width, _, _ = term.GetSize(int(file.Fd()))
						}
						state = inputYMODEMUploadPath
						bar.update("", nil, time.Time{})
						fmt.Fprint(f.cfg.Local, "\r\n\x1b[?2004h")
						path.render(f.cfg.Local)
					case actionYMODEMDownload:
						progress = transferProgress{started: time.Now()}
						ymodemMode = true
						printLocalLine(f.cfg.Local, "")
						if err := endpoint.StartYMODEMDownload(ctx, "."); err != nil {
							ymodemMode = false
							printLocalLine(f.cfg.Local, localColor(fmt.Sprintf("YMODEM download failed: %v", err), localErrorColor))
						} else {
							state = inputYMODEMTransfer
							bar.update("", nil, progress.started)
							printLocalLine(f.cfg.Local, localColor("Waiting for sender (Esc to cancel)", localInfoColor))
						}
					case actionQuit:
						printLocalLine(f.cfg.Local, "")
						endpoint.Quit()
						return nil
					}
				} else {
					if err := endpoint.Send(ctx, []byte{f.cfg.PrefixKey, result.b}); err != nil && !errors.Is(err, session.ErrDisconnected) {
						return err
					}
				}
			case inputYMODEMUploadPath:
				// A bare Esc followed by ordinary input cancels selection, then lets that
				// next byte resume normal serial input. CSI/SS3 sequences stay local.
				if len(path.pending) == 1 && path.pending[0] == 0x1b && !path.paste && result.b != '[' && result.b != 'O' {
					escapeTimer.Stop()
					cancelSelection()
					if result.b == f.cfg.PrefixKey {
						state = inputAfterPrefix
					} else if err := endpoint.Send(ctx, []byte{result.b}); err != nil && !errors.Is(err, session.ErrDisconnected) {
						return err
					}
					requestRead()
					continue
				}
				submit, canceled := path.feed(result.b, f.cfg.Local)
				escapeTimer.Stop()
				if len(path.pending) > 0 && path.pending[0] == 0x1b {
					escapeTimer.Reset(100 * time.Millisecond)
				}
				if canceled {
					cancelSelection()
				}
				if submit {
					name, err := inputPath(string(path.line))
					if err == nil && name == "" {
						cancelSelection()
						break
					}
					if err == nil {
						var info os.FileInfo
						info, err = os.Stat(name)
						if err == nil && !info.Mode().IsRegular() {
							err = fmt.Errorf("not a regular file: %s", name)
						}
					}
					if err == nil {
						progress = transferProgress{started: time.Now()}
						err = endpoint.StartYMODEMUpload(ctx, name)
					}
					printLocalLine(f.cfg.Local, "")
					if err != nil {
						printLocalLine(f.cfg.Local, localColor(fmt.Sprintf("YMODEM upload failed: %v", err), localErrorColor))
						path.render(f.cfg.Local)
					} else {
						lastUploadPath = string(path.line)
						fmt.Fprint(f.cfg.Local, "\x1b[?2004l")
						state = inputYMODEMTransfer
						bar.update("", nil, progress.started)
						printLocalLine(f.cfg.Local, localColor("Waiting for receiver (Esc to cancel)", localInfoColor))
					}
				}
			case inputYMODEMTransfer:
				if result.b == 0x1b {
					endpoint.CancelTransfer()
					state = inputCancellingTransfer
					bar.update("", nil, time.Time{})
					progress.endLine(f.cfg.Local)
					printLocalLine(f.cfg.Local, localColor("\rCanceling transfer", localWarningColor))
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
	printLocalLine(w, "Commands:")
	for _, command := range localCommands {
		printLocalLine(w, fmt.Sprintf("  Ctrl-P %-7s %s", command.label, command.description))
	}
	printLocalLine(w, "  Ctrl-P Ctrl-P  send Ctrl-P")
	printLocalLine(w, "  Esc            cancel file selection or transfer")
	printLocalLine(w, "  Ctrl-C         clear the entire upload path during file selection")
	printLocalLine(w, "  Tab            complete upload path (press again to list matches)")
}

func formatYMODEMResult(event session.YMODEMFinished) string {
	action := "Sent"
	if event.Direction == "download" {
		action = "Received"
	}
	name := filepath.Base(event.Path)
	result := fmt.Sprintf("✓ %s %s  ·  %s  ·  CRC32 %08x", action, name, transferSize(float64(event.Bytes)), event.CRC32)
	if event.RetriedFrames > 0 {
		result += fmt.Sprintf("  ·  %d retries", event.RetriedFrames)
	}
	return result
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
