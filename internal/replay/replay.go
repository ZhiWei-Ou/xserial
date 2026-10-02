// Package replay plays recorded traffic without opening a serial port.
package replay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/rawui"
	"github.com/ZhiWei-Ou/xserial/internal/transfer"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const controls = "Space pause/resume · r restart · Ctrl-C quit"

type Config struct {
	Input    io.Reader
	Output   io.Writer
	Local    io.Writer
	Terminal rawui.Terminal
	Hexdump  bool
	Framing  FrameConfig
	Size     func() (width, height int)
}

type inputEvent struct {
	key byte
	err error
}

// Run leaves completed playback open while terminal input is available. With
// redirected input it plays once and exits, without inserting terminal controls
// into native RX output. Pausing preserves the remaining event interval.
func Run(ctx context.Context, cfg Config, session capture.Session) (runErr error) {
	if cfg.Output == nil {
		return errors.New("replay output is required")
	}
	if cfg.Local == nil {
		cfg.Local = io.Discard
	}
	interactive := cfg.Terminal != nil
	var input <-chan inputEvent
	if interactive {
		if cfg.Input == nil {
			return errors.New("replay terminal input is required")
		}
		if err := cfg.Terminal.MakeRaw(); err != nil {
			return fmt.Errorf("enter raw mode: %w", err)
		}
		restoreOutput, err := prepareOutput(cfg.Output)
		if err != nil {
			return errors.Join(err, cfg.Terminal.Restore())
		}
		defer func() {
			runErr = errors.Join(runErr, transfer.WriteFull(cfg.Output, []byte("\x1b[0m\x1b[?25h\r\n")), restoreOutput(), cfg.Terminal.Restore())
		}()
		reader, err := uv.NewCancelReader(cfg.Input)
		if err != nil {
			return fmt.Errorf("create replay input reader: %w", err)
		}
		readCtx, cancel := context.WithCancel(ctx)
		events := make(chan inputEvent, 8)
		done := make(chan struct{})
		go func() {
			defer close(done)
			readInput(readCtx, reader, events)
		}()
		defer func() {
			cancel()
			reader.Cancel()
			<-done
			runErr = errors.Join(runErr, reader.Close())
		}()
		input = events
	}

	var hex *hexScreen
	if cfg.Hexdump {
		hex = newHexScreen(cfg)
	}
	status := func(state string) error {
		if hex != nil {
			if interactive || state == "complete" {
				return hex.render(state)
			}
			return nil
		}
		if interactive {
			_, err := fmt.Fprintf(cfg.Local, "\r\nReplay %s · %s\r\n", state, controls)
			return err
		}
		return nil
	}
	if err := status("playing"); err != nil {
		return err
	}

	timer := time.NewTimer(0)
	defer timer.Stop()
	var tick <-chan time.Time
	var deadline time.Time
	var remaining time.Duration
	index, paused := 0, false
	schedule := func() {
		previous := session.Header.Started
		if index > 0 {
			previous = session.Records[index-1].At
		}
		remaining = max(0, session.Records[index].At.Sub(previous))
		deadline = time.Now().Add(remaining)
		timer.Reset(remaining)
		tick = timer.C
	}
	if len(session.Records) > 0 {
		schedule()
	} else if err := status("complete"); err != nil {
		return err
	}

	for {
		if index == len(session.Records) && input == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case event := <-input:
			if event.err != nil {
				if errors.Is(event.err, io.EOF) {
					input = nil
					if paused {
						paused = false
						deadline = time.Now().Add(remaining)
						timer.Reset(remaining)
						tick = timer.C
					}
					continue
				}
				return fmt.Errorf("read replay input: %w", event.err)
			}
			switch event.key {
			case 3:
				return nil
			case ' ':
				if index == len(session.Records) {
					continue
				}
				paused = !paused
				state := "paused"
				if paused {
					remaining = max(0, time.Until(deadline))
					timer.Stop()
					tick = nil
				} else {
					deadline = time.Now().Add(remaining)
					timer.Reset(remaining)
					tick = timer.C
					state = "playing"
				}
				if err := status(state); err != nil {
					return err
				}
			case 'r', 'R':
				timer.Stop()
				tick = nil
				index, paused = 0, false
				if hex != nil {
					hex = newHexScreen(cfg)
				} else if err := transfer.WriteFull(cfg.Output, []byte("\x1b[0m\x1b[2J\x1b[H")); err != nil {
					return err
				}
				if err := status("playing"); err != nil {
					return err
				}
				if len(session.Records) > 0 {
					schedule()
				} else if err := status("complete"); err != nil {
					return err
				}
			}
		case <-tick:
			record := session.Records[index]
			if hex != nil {
				if err := hex.record(record); err != nil {
					return err
				}
			} else if record.Kind == "rx" {
				if err := transfer.WriteFull(cfg.Output, record.Data); err != nil {
					return fmt.Errorf("write recorded output: %w", err)
				}
			}
			index++
			if index == len(session.Records) {
				tick = nil
				if err := status("complete"); err != nil {
					return err
				}
			} else {
				if hex != nil && interactive && (record.Kind == "rx" || record.Kind == "tx") && len(record.Data) > 0 {
					if err := hex.render("playing"); err != nil {
						return err
					}
				}
				schedule()
			}
		}
	}
}

// Parse input sequences so terminal replies such as ESC[1;20R do not invoke
// restart. Only the three playback controls produce events.
func readInput(ctx context.Context, reader io.Reader, events chan<- inputEvent) {
	emit := func(event inputEvent) {
		select {
		case events <- event:
		case <-ctx.Done():
		}
	}
	parser := ansi.NewParser()
	parser.SetHandler(ansi.Handler{
		Print: func(r rune) {
			if r == ' ' || r == 'r' || r == 'R' {
				emit(inputEvent{key: byte(r)})
			}
		},
		Execute: func(b byte) {
			if b == 3 {
				emit(inputEvent{key: b})
			}
		},
	})
	var buffer [256]byte
	for {
		n, err := reader.Read(buffer[:])
		for _, b := range buffer[:n] {
			parser.Advance(b)
		}
		if err != nil {
			emit(inputEvent{err: err})
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}
