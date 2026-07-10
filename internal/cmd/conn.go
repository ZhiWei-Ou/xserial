package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/ZhiWei-Ou/xserial/internal/logging"
	"github.com/ZhiWei-Ou/xserial/internal/rawui"
	"github.com/ZhiWei-Ou/xserial/internal/serialport"
	"github.com/ZhiWei-Ou/xserial/internal/session"
	serialtui "github.com/ZhiWei-Ou/xserial/internal/tui"
	"github.com/spf13/cobra"
)

type connOptions struct {
	port       string
	baud       int
	dataBits   int
	parity     string
	stopBits   string
	logPath    string
	timeFormat string
	tui        bool
}

const defaultConnBaud = 115200

func NewConnCommand() *cobra.Command {
	var cfg string
	var logPath string
	var timeFormat string
	var useTUI bool

	connCmd := &cobra.Command{
		Use:     "conn <port> [baud]",
		Short:   "Connect to a serial port",
		Example: "  xserial conn /dev/tty.usbserial\n  xserial conn /dev/tty.usbserial 9600 -c 8,N,1",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				_ = cmd.Help()
				return errors.New("serial port is required")
			}
			return cobra.RangeArgs(1, 2)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := parseConnOptions(args, cfg, logPath, timeFormat, useTUI)
			if err != nil {
				return err
			}
			return runConn(cmd.Context(), opts)
		},
	}

	connCmd.Flags().StringVarP(&cfg, "cfg", "c", "8,N,1", "serial frame as data-bits,parity,stop-bits (example: 8,N|n,1)")
	connCmd.Flags().StringVar(&logPath, "log", "", "append received bytes to file")
	connCmd.Flags().StringVar(&timeFormat, "time", "", "Go time format prepended to each received line")
	connCmd.Flags().BoolVar(&useTUI, "tui", false, "open the modern full-screen interface")

	return connCmd
}

func parseConnOptions(args []string, cfg, logPath, timeFormat string, useTUI bool) (connOptions, error) {
	opts := connOptions{
		port:       args[0],
		baud:       defaultConnBaud,
		logPath:    logPath,
		timeFormat: timeFormat,
		tui:        useTUI,
	}

	if len(args) == 2 {
		baud, err := strconv.Atoi(args[1])
		if err != nil || baud <= 0 {
			return connOptions{}, fmt.Errorf("invalid baud rate %q", args[1])
		}
		opts.baud = baud
	}

	fields := strings.Split(cfg, ",")
	if len(fields) != 3 {
		return connOptions{}, fmt.Errorf("invalid serial cfg %q: want data-bits,parity,stop-bits", cfg)
	}

	dataBits, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil || dataBits <= 0 {
		return connOptions{}, fmt.Errorf("invalid data bits %q", fields[0])
	}
	opts.dataBits = dataBits

	switch strings.ToUpper(strings.TrimSpace(fields[1])) {
	case "N":
		opts.parity = "none"
	case "O":
		opts.parity = "odd"
	case "E":
		opts.parity = "even"
	case "M":
		opts.parity = "mark"
	case "S":
		opts.parity = "space"
	default:
		return connOptions{}, fmt.Errorf("invalid parity %q: want N, O, E, M, or S", fields[1])
	}

	opts.stopBits = strings.TrimSpace(fields[2])
	if opts.stopBits != "1" && opts.stopBits != "1.5" && opts.stopBits != "2" {
		return connOptions{}, fmt.Errorf("invalid stop bits %q: want 1, 1.5, or 2", fields[2])
	}

	return opts, nil
}

func runConn(ctx context.Context, opts connOptions) error {
	logger := logging.New(os.Stderr)
	port, err := serialport.Open(opts.port, serialport.Config{
		BaudRate: opts.baud,
		DataBits: opts.dataBits,
		Parity:   opts.parity,
		StopBits: opts.stopBits,
	})
	if err != nil {
		return fmt.Errorf("open serial port %q: %w", opts.port, err)
	}

	receiveLog, err := openReceiveLog(opts.logPath)
	if err != nil {
		_ = port.Close()
		return err
	}
	if receiveLog != nil {
		defer receiveLog.Close()
	}

	logger.Info(
		"session.connected",
		"port", opts.port,
		"baud", opts.baud,
		"data_bits", opts.dataBits,
		"parity", opts.parity,
		"stop_bits", opts.stopBits,
	)
	var frontend session.Frontend
	if opts.tui {
		logger.Info("session.ready", "mode", "tui", "commands", "Ctrl-P", "quit", "Ctrl-C")
		frontend = serialtui.New(serialtui.Config{
			Input:      os.Stdin,
			Output:     os.Stdout,
			TimeFormat: opts.timeFormat,
			PortName:   opts.port,
			Baud:       opts.baud,
			Frame:      fmt.Sprintf("%d,%s,%s", opts.dataBits, strings.ToUpper(opts.parity[:1]), opts.stopBits),
		})
	} else {
		logger.Info("session.ready", "mode", "raw", "prefix", "Ctrl-P", "help", "Ctrl-P h", "quit", "Ctrl-P q")
		frontend = rawui.New(rawui.Config{
			Terminal: rawui.NewOSTerminal(os.Stdin),
			Input:    os.Stdin,
			Output:   os.Stdout,
			Local:    os.Stderr,
		})
	}

	s := session.New(session.Config{
		Port:              port,
		Frontend:          frontend,
		ReceiveLog:        receiveLog,
		ReceiveTimeFormat: opts.timeFormat,
		Logger:            logger,
	})
	return s.Run(ctx)
}

func openReceiveLog(path string) (io.WriteCloser, error) {
	if path == "" {
		return nil, nil
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open receive log %q: %w", path, err)
	}
	return file, nil
}
