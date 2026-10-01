package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/demo"
	"github.com/ZhiWei-Ou/xserial/internal/mcpdaemon"
	"github.com/ZhiWei-Ou/xserial/internal/mcpserver"
	"github.com/ZhiWei-Ou/xserial/internal/serialport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMCPCommand() *cobra.Command {
	opts := mcpdaemon.Options{Version: currentVersion()}
	cmd := &cobra.Command{
		Use: "mcp", Short: "Serve device debugging tools over MCP stdio", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := mcpdaemon.Ensure(cmd.Context(), opts)
			if err != nil {
				return err
			}
			defer client.Close()
			return mcpserver.New(opts.Version, client).Run(cmd.Context(), &mcp.StdioTransport{MaxLineLength: 1 << 20})
		},
	}
	cmd.PersistentFlags().StringVar(&opts.StateDir, "state-dir", "", "private daemon state directory (default: per-user cache)")
	cmd.PersistentFlags().BoolVar(&opts.Demo, "demo", false, "use an isolated simulated device daemon")
	cmd.AddCommand(&cobra.Command{
		Use: "daemon", Short: "Run the unique device daemon in the foreground", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcpdaemon.Run(cmd.Context(), opts, deviceDependencies(opts.Demo))
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "stop", Short: "Stop the device daemon and close its serial connection", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return mcpdaemon.Stop(cmd.Context(), opts) },
	})
	cmd.AddCommand(&cobra.Command{
		Use: "status", Short: "Show the running daemon and serial connection as JSON", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := mcpdaemon.StateDirectory(opts.StateDir, opts.Demo)
			if err != nil {
				return err
			}
			opts.StateDir = dir
			client, err := mcpdaemon.Connect(cmd.Context(), opts)
			if err != nil {
				return err
			}
			defer client.Close()
			var status debugsession.StatusResult
			if err := client.Call(cmd.Context(), "status", struct{}{}, &status); err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				PID     int                       `json:"pid"`
				Version string                    `json:"version"`
				Session debugsession.StatusResult `json:"session"`
			}{client.Meta.PID, client.Meta.Version, status})
		},
	})
	return cmd
}

func deviceDependencies(simulated bool) debugsession.Dependencies {
	if simulated {
		return debugsession.Dependencies{
			List: func() ([]serialport.Info, error) {
				return []serialport.Info{{Name: "demo", Product: "Simulated Modbus device"}}, nil
			},
			Open: func(_ context.Context, cfg debugsession.Connection) (io.ReadWriteCloser, error) {
				if cfg.Port != "demo" {
					return nil, errors.New("demo daemon only supports port demo")
				}
				return demo.NewPort(), nil
			},
		}
	}
	return debugsession.Dependencies{List: serialport.List,
		Open: func(_ context.Context, cfg debugsession.Connection) (io.ReadWriteCloser, error) {
			parity := map[string]string{"N": "none", "O": "odd", "E": "even", "M": "mark", "S": "space"}[cfg.Parity]
			return serialport.Open(cfg.Port, serialport.Config{BaudRate: cfg.Baud, DataBits: cfg.DataBits, Parity: parity, StopBits: cfg.StopBits})
		},
	}
}
