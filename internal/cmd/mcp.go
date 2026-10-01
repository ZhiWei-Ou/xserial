package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/mcpdaemon"
	"github.com/ZhiWei-Ou/xserial/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func newMCPCommand() *cobra.Command {
	opts := mcpdaemon.Options{Version: currentVersion(), URL: mcpdaemon.DefaultURL}
	var transport string
	cmd := &cobra.Command{
		Use: "mcp", Short: "Serve attached terminal sessions over MCP HTTP", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.Stderr = cmd.ErrOrStderr()
			if _, err := mcpdaemon.ParseURL(opts.URL); err != nil {
				return err
			}
			switch transport {
			case "http":
				return mcpdaemon.Run(cmd.Context(), opts)
			case "stdio":
				client, err := mcpdaemon.Ensure(cmd.Context(), opts)
				if err != nil {
					return err
				}
				defer client.Close()
				ctx, cancel := context.WithCancel(cmd.Context())
				defer cancel()
				auditDone := make(chan struct{})
				if !client.Started {
					var checkpoint mcpdaemon.AuditResult
					if err := client.Call(ctx, "audit", mcpdaemon.AuditInput{Now: true}, &checkpoint); err != nil {
						return err
					}
					go func() { defer close(auditDone); _ = client.FollowAudit(ctx, checkpoint.Next, cmd.ErrOrStderr()) }()
				} else {
					close(auditDone)
				}
				err = mcpserver.New(opts.Version, client).Run(ctx, &mcp.StdioTransport{MaxLineLength: 1 << 20})
				cancel()
				<-auditDone
				return err
			default:
				return fmt.Errorf("invalid MCP transport %q: want http or stdio", transport)
			}
		},
	}
	cmd.PersistentFlags().StringVar(&opts.StateDir, "state-dir", "", "private session and daemon state directory (default: per-user cache)")
	cmd.PersistentFlags().StringVar(&opts.URL, "url", mcpdaemon.DefaultURL, "local HTTP MCP URL")
	cmd.PersistentFlags().BoolVar(&opts.Demo, "demo", false, "use the isolated demo session namespace")
	cmd.Flags().StringVar(&transport, "transport", "http", "MCP transport: http or stdio")
	cmd.AddCommand(&cobra.Command{
		Use: "daemon", Short: "Run the unique MCP HTTP daemon in the foreground", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.Stderr = cmd.ErrOrStderr()
			return mcpdaemon.Run(cmd.Context(), opts)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use: "stop", Short: "Stop MCP access while leaving terminal serial sessions running", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return mcpdaemon.Stop(cmd.Context(), opts) },
	})
	cmd.AddCommand(&cobra.Command{
		Use: "status", Short: "Show the running daemon URL and attached sessions as JSON", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
				PID      int                   `json:"pid"`
				Version  string                `json:"version"`
				URL      string                `json:"url"`
				Sessions []debugsession.Status `json:"sessions"`
			}{client.Meta.PID, client.Meta.Version, client.Meta.URL, status.Sessions})
		},
	})
	return cmd
}

func publishTerminal(ctx context.Context, dir string, demo bool, debug *debugsession.Session) (*mcpdaemon.Publication, error) {
	publication, err := mcpdaemon.Publish(ctx, mcpdaemon.Options{StateDir: dir, Demo: demo, Version: currentVersion()}, debug)
	if err != nil {
		return nil, fmt.Errorf("publish terminal session: %w", err)
	}
	return publication, nil
}

// A disabled sidecar is diagnosed before entering any full-screen UI. Once
// running, its lifecycle cannot alter terminal I/O or terminal restoration.
func closePublication(publication *mcpdaemon.Publication) error {
	if publication == nil {
		return nil
	}
	return publication.Close()
}
