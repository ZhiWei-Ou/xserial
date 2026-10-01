package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/logging"
	session "github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/ZhiWei-Ou/xserial/internal/rawui"
	"github.com/ZhiWei-Ou/xserial/internal/serialport"
	serialtui "github.com/ZhiWei-Ou/xserial/internal/tui"
	"github.com/ZhiWei-Ou/xserial/internal/workbench"
	"github.com/spf13/cobra"
)

type connOptions struct {
	stateDir      string
	port          string
	baud          int
	dataBits      int
	parity        string
	stopBits      string
	logPath       string
	timeFormat    string
	tui           bool
	hexdump       bool
	workbench     bool
	favoritesPath string
	framing       hexdata.FrameConfig
	recordPath    string
}

type connFlags struct {
	stateDir      string
	logPath       string
	showTime      bool
	useTUI        bool
	hexdump       bool
	workbench     bool
	favoritesPath string
	frameRule     string
	recordPath    string
}

const (
	defaultConnBaud          = 115200
	defaultReceiveTimeFormat = "15:04:05.000"
)

func bindConnFlags(cmd *cobra.Command, flags *connFlags) {
	cmd.Flags().StringVar(&flags.stateDir, "state-dir", "", "private state directory for MCP session discovery")
	cmd.Flags().Bool("help", false, "help for xserial")
	cmd.Flags().BoolVarP(&flags.hexdump, "hexdump", "h", false, "display received bytes as a hex and ASCII dump")
	cmd.Flags().StringVar(&flags.logPath, "log", "", "append received bytes to file")
	cmd.Flags().BoolVarP(&flags.showTime, "time", "t", false, "prepend timestamps (HH:MM:SS.mmm) to received lines")
	cmd.Flags().BoolVar(&flags.useTUI, "TUI", false, "open the full-screen interface (Beta, unstable)")
	cmd.Flags().BoolVar(&flags.workbench, "workbench", false, "open the binary serial debugging workbench")
	cmd.Flags().StringVar(&flags.favoritesPath, "commands", "", "command favorites JSON file (default: user config directory)")
	cmd.Flags().StringVar(&flags.frameRule, "frame", "chunk", "RX framing: chunk, fixed:N, delimiter:HEX, length:OFFSET:WIDTH:OVERHEAD:le|be, modbus-read")
	cmd.Flags().StringVar(&flags.recordPath, "record", "", "record original RX/TX and connection events to a new .xsr file")
	cmd.MarkFlagsMutuallyExclusive("hexdump", "TUI")
	cmd.MarkFlagsMutuallyExclusive("workbench", "TUI")
	cmd.MarkFlagsMutuallyExclusive("workbench", "hexdump")
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

func runConn(ctx context.Context, opts connOptions) (runErr error) {
	var favorites []workbench.Favorite
	if opts.workbench {
		var err error
		opts.favoritesPath, favorites, err = loadCommandFavorites(opts.favoritesPath)
		if err != nil {
			return err
		}
	}
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
	recordFile, recorder, err := openRecording(opts.recordPath, connectionConfig)
	if err != nil {
		_ = port.Close()
		return err
	}
	if recordFile != nil {
		defer func() { runErr = errors.Join(runErr, recordFile.Close()) }()
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
	if opts.workbench {
		sessionLogger = nil
		frontend = workbench.New(workbench.Config{
			Input: os.Stdin, Output: os.Stdout, Connection: connectionConfig,
			FavoritesPath: opts.favoritesPath, Favorites: favorites,
			Framing:  opts.framing,
			Recorder: recorder,
		})
	} else if opts.tui {
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

	debug := debugsession.New(debugsession.Connection{})
	publication, publishErr := publishTerminal(ctx, opts.stateDir, false, debug)
	if publishErr != nil {
		logger.Warn("mcp.publish_failed", "error", publishErr)
	}
	defer func() { runErr = errors.Join(runErr, closePublication(publication)) }()
	s := session.New(session.Config{
		Connection: connectionConfig, Debug: debug,
		Port:              port,
		Reconnect:         openPort,
		ReconnectInterval: time.Second,
		OpenConnection:    openConnection,
		Frontend:          frontend,
		ReceiveLog:        receiveLog,
		ReceiveTimeFormat: opts.timeFormat,
		Logger:            sessionLogger,
		Recorder:          recorder,
	})
	return s.Run(ctx)
}

func openRecording(path string, cfg session.ConnectionConfig) (*os.File, *capture.Writer, error) {
	if path == "" {
		return nil, nil, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create recording %q: %w", path, err)
	}
	recorder, err := capture.NewWriter(file, capture.Header{Port: cfg.PortName, Baud: cfg.BaudRate, DataBits: cfg.DataBits, Parity: cfg.Parity, StopBits: cfg.StopBits})
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, recorder, nil
}

func loadCommandFavorites(path string) (string, []workbench.Favorite, error) {
	if path == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", nil, fmt.Errorf("locate command favorites: %w", err)
		}
		path = filepath.Join(dir, "xserial", "commands.json")
	}
	favorites, err := workbench.LoadFavorites(path)
	return path, favorites, err
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
