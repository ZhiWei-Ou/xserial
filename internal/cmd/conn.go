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
	port       string
	baud       int
	dataBits   int
	parity     string
	stopBits   string
	logPath    string
	timeFormat string
	tui        bool
	hexdump    bool
}

type connFlags struct {
	logPath  string
	showTime bool
	useTUI   bool
	hexdump  bool
}

const (
	defaultConnBaud          = 115200
	defaultReceiveTimeFormat = "15:04:05.000"
)

func bindConnFlags(cmd *cobra.Command, flags *connFlags) {
	cmd.Flags().Bool("help", false, "help for xserial")
	cmd.Flags().BoolVarP(&flags.hexdump, "hexdump", "h", false, "display received bytes as a hex and ASCII dump")
	cmd.Flags().StringVar(&flags.logPath, "log", "", "append received bytes to file")
	cmd.Flags().BoolVarP(&flags.showTime, "time", "t", false, "prepend timestamps (HH:MM:SS.mmm) to received lines")
	cmd.Flags().BoolVar(&flags.useTUI, "TUI", false, "open the full-screen interface (Beta, unstable)")
	cmd.MarkFlagsMutuallyExclusive("hexdump", "TUI")
}

func parseConnOptions(args []string, logPath string, showTime, useTUI bool) (connOptions, error) {
	opts := connOptions{
		port:     args[0],
		baud:     defaultConnBaud,
		dataBits: 8,
		parity:   "none",
		stopBits: "1",
		logPath:  logPath,
		tui:      useTUI,
	}

	if showTime {
		opts.timeFormat = defaultReceiveTimeFormat
	}

	if len(args) == 1 {
		return opts, nil
	}

	cfg := args[1]
	fields := strings.Split(cfg, ",")
	if len(fields) > 4 {
		return connOptions{}, fmt.Errorf("invalid serial cfg %q: want baud[,data-bits[,parity[,stop-bits]]]", cfg)
	}

	if len(fields) >= 1 {
		baud, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || baud <= 0 {
			return connOptions{}, fmt.Errorf("invalid baud rate %q", fields[0])
		}
		opts.baud = baud
	}

	if len(fields) >= 2 {
		dataBits, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil || dataBits <= 0 {
			return connOptions{}, fmt.Errorf("invalid data bits %q", fields[1])
		}
		opts.dataBits = dataBits
	}

	if len(fields) >= 3 {
		switch strings.ToUpper(strings.TrimSpace(fields[2])) {
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
			return connOptions{}, fmt.Errorf("invalid parity %q: want N, O, E, M, or S", fields[2])
		}
	}

	if len(fields) >= 4 {
		opts.stopBits = strings.TrimSpace(fields[3])
		if opts.stopBits != "1" && opts.stopBits != "1.5" && opts.stopBits != "2" {
			return connOptions{}, fmt.Errorf("invalid stop bits %q: want 1, 1.5, or 2", fields[3])
		}
	}

	return opts, nil
}

func runConn(ctx context.Context, opts connOptions) error {
	logger := logging.New(os.Stderr)
	connectionConfig := session.ConnectionConfig{
		PortName: opts.port,
		BaudRate: opts.baud,
		DataBits: opts.dataBits,
		Parity:   opts.parity,
		StopBits: opts.stopBits,
	}
	openConnection := func(cfg session.ConnectionConfig) (session.SerialPort, error) {
		return serialport.Open(cfg.PortName, serialport.Config{
			BaudRate: cfg.BaudRate,
			DataBits: cfg.DataBits,
			Parity:   cfg.Parity,
			StopBits: cfg.StopBits,
		})
	}
	openPort := func() (session.SerialPort, error) { return openConnection(connectionConfig) }
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

	var frontend session.Frontend
	sessionLogger := logger
	if opts.tui {
		// The TUI renders transfer status itself; background stderr writes would
		// corrupt Bubble Tea's alternate-screen output.
		sessionLogger = nil
		frontend = serialtui.New(serialtui.Config{
			Input:      os.Stdin,
			Output:     os.Stdout,
			TimeFormat: opts.timeFormat,
			PortName:   opts.port,
			Baud:       opts.baud,
			DataBits:   opts.dataBits,
			Parity:     opts.parity,
			StopBits:   opts.stopBits,
			ListPorts: func() ([]serialtui.PortOption, error) {
				ports, err := serialport.List()
				if err != nil {
					return nil, err
				}
				options := make([]serialtui.PortOption, 0, len(ports))
				for _, port := range ports {
					detail := strings.TrimSpace(strings.TrimPrefix(serialport.FormatInfo(port), port.Name))
					options = append(options, serialtui.PortOption{Name: port.Name, Detail: detail})
				}
				return options, nil
			},
		})
	} else {
		frontend = rawui.New(rawui.Config{
			Connection: connectionConfig,
			Terminal:   rawui.NewOSTerminal(os.Stdin),
			Hexdump:    opts.hexdump,
			Input:      os.Stdin,
			Output:     os.Stdout,
			Local:      logger,
			TimeFormat: opts.timeFormat,
		})
	}

	s := session.New(session.Config{
		Port:              port,
		Reconnect:         openPort,
		ReconnectInterval: time.Second,
		OpenConnection:    openConnection,
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
