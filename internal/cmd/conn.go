package cmd

import (
	"context"
	"os"

	"github.com/ZhiWei-Ou/xserial/internal/logging"
	"github.com/ZhiWei-Ou/xserial/internal/serialport"
	"github.com/ZhiWei-Ou/xserial/internal/session"
	"github.com/spf13/cobra"
)

type connOptions struct {
	port     string
	baud     int
	dataBits int
	parity   string
	stopBits string
}

func NewConnCommand() *cobra.Command {
	opts := connOptions{
		baud:     115200,
		dataBits: 8,
		parity:   "none",
		stopBits: "1",
	}

	connCmd := &cobra.Command{
		Use:   "conn <port>",
		Short: "Connect to a serial port",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.port = args[0]
			return runConn(cmd.Context(), opts)
		},
	}

	connCmd.Flags().IntVarP(&opts.baud, "baud", "b", opts.baud, "baud rate")
	connCmd.Flags().IntVar(&opts.dataBits, "data-bits", opts.dataBits, "data bits")
	connCmd.Flags().StringVar(&opts.parity, "parity", opts.parity, "parity: none, odd, even, mark, space")
	connCmd.Flags().StringVar(&opts.stopBits, "stop-bits", opts.stopBits, "stop bits: 1, 1.5, 2")

	return connCmd
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
		return err
	}

	logger.Info(
		"session.connected",
		"port", opts.port,
		"baud", opts.baud,
		"data_bits", opts.dataBits,
		"parity", opts.parity,
		"stop_bits", opts.stopBits,
	)
	logger.Info("session.ready", "prefix", "Ctrl-A", "help", "Ctrl-A h", "quit", "Ctrl-A q")

	s := session.New(session.Config{
		Port:     port,
		Terminal: session.NewOSTerminal(os.Stdin),
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Logger:   logger,
	})
	return s.Run(ctx)
}
