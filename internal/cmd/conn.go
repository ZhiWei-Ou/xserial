package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/logging"
	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/ZhiWei-Ou/xserial/internal/rawui"
	"github.com/ZhiWei-Ou/xserial/internal/serialport"
	serialtui "github.com/ZhiWei-Ou/xserial/internal/tui"
	"github.com/spf13/cobra"
)

type connOptions struct {
	port              string
	baud              int
	dataBits          int
	parity            string
	stopBits          string
	logPath           string
	timeFormat        string
	tui               bool
	reconnectAttempts int
}

type connFlags struct {
	cfg               string
	logPath           string
	timeFormat        string
	useTUI            bool
	reconnectAttempts int
}

const (
	defaultConnBaud          = 115200
	defaultReceiveTimeFormat = "15:04:05.000"
)

func bindConnFlags(cmd *cobra.Command, flags *connFlags) {
	cmd.Flags().StringVarP(&flags.cfg, "cfg", "c", "8,N,1", "serial frame as data-bits,parity,stop-bits (example: 8,N|n,1)")
	cmd.Flags().StringVar(&flags.logPath, "log", "", "append received bytes to file")
	cmd.Flags().StringVar(&flags.timeFormat, "time", "", "Go time format prepended to each received line")
	cmd.Flags().Lookup("time").NoOptDefVal = defaultReceiveTimeFormat
	cmd.Flags().BoolVar(&flags.useTUI, "tui", false, "open the modern full-screen interface")
	cmd.Flags().IntVar(&flags.reconnectAttempts, "reconnect", 5, "number of reconnect attempts after disconnection (0 disables)")
}

func parseConnOptions(args []string, cfg, logPath, timeFormat string, useTUI bool) (connOptions, error) {
	opts := connOptions{
		port:              args[0],
		baud:              defaultConnBaud,
		logPath:           logPath,
		timeFormat:        timeFormat,
		tui:               useTUI,
		reconnectAttempts: 5,
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
	serialConfig := serialport.Config{
		BaudRate: opts.baud,
		DataBits: opts.dataBits,
		Parity:   opts.parity,
		StopBits: opts.stopBits,
	}
	openPort := func() (session.SerialPort, error) {
		return serialport.Open(opts.port, serialConfig)
	}
	port, err := openPort()
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
	var sessionLogger session.Logger = logger
	if opts.tui {
		logger.Info("session.ready", "help", "Ctrl-P", "quit", "Ctrl-C")
		// The TUI renders transfer status itself; background stderr writes would
		// corrupt Bubble Tea's alternate-screen output.
		sessionLogger = nil
		frontend = serialtui.New(serialtui.Config{
			Input:      os.Stdin,
			Output:     os.Stdout,
			TimeFormat: opts.timeFormat,
			PortName:   opts.port,
			Baud:       opts.baud,
			Frame:      fmt.Sprintf("%d,%s,%s", opts.dataBits, strings.ToUpper(opts.parity[:1]), opts.stopBits),
		})
	} else {
		logger.Info("session.ready", "help", "Ctrl-P h", "quit", "Ctrl-P q")
		frontend = rawui.New(rawui.Config{
			Terminal:   rawui.NewOSTerminal(os.Stdin),
			Input:      os.Stdin,
			Output:     os.Stdout,
			Local:      os.Stderr,
			TimeFormat: opts.timeFormat,
		})
	}

	s := session.New(session.Config{
		Port:              port,
		Reconnect:         openPort,
		ReconnectInterval: time.Second,
		ReconnectAttempts: func() int {
			if opts.reconnectAttempts == 0 {
				return -1
			}
			return opts.reconnectAttempts
		}(),
		Frontend:          frontend,
		ReceiveLog:        receiveLog,
		ReceiveTimeFormat: opts.timeFormat,
		Logger:            sessionLogger,
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
