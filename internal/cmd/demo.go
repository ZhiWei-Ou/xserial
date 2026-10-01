package cmd

import (
	"errors"
	"os"

	"github.com/ZhiWei-Ou/xserial/internal/demo"
	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
	"github.com/ZhiWei-Ou/xserial/internal/workbench"
	"github.com/spf13/cobra"
)

func newDemoCommand() *cobra.Command {
	var commandsPath string
	var frameRule string
	var recordPath string
	var snapshot bool
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Try the binary workbench without serial hardware",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (runErr error) {
			framing, err := hexdata.ParseFrameConfig(frameRule)
			if err != nil {
				return err
			}
			path, favorites, err := loadCommandFavorites(commandsPath)
			if err != nil {
				return err
			}
			port := demo.NewPort()
			cfg := middleware.ConnectionConfig{PortName: "simulated-device", BaudRate: 115200, DataBits: 8, Parity: "none", StopBits: "1"}
			recordFile, recorder, err := openRecording(recordPath, cfg)
			if err != nil {
				_ = port.Close()
				return err
			}
			if recordFile != nil {
				defer func() { runErr = errors.Join(runErr, recordFile.Close()) }()
			}
			workbenchConfig := workbench.Config{
				Input: cmd.InOrStdin(), Output: cmd.OutOrStdout(), Connection: cfg,
				FavoritesPath: path, Favorites: favorites, Demo: true, Framing: framing, Recorder: recorder,
			}
			var frontend middleware.Frontend = workbench.New(workbenchConfig)
			if snapshot {
				frontend = workbench.NewPreview(workbenchConfig)
			}
			return middleware.New(middleware.Config{Port: port, Frontend: frontend, Recorder: recorder}).Run(cmd.Context())
		},
	}
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.Flags().StringVar(&commandsPath, "commands", "", "command favorites JSON file")
	cmd.Flags().StringVar(&frameRule, "frame", "chunk", "RX framing rule; modbus-read reassembles the example responses")
	cmd.Flags().StringVar(&recordPath, "record", "", "record the demo to a new .xsr file")
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "run one demo query and print the workbench without interactive terminal mode")
	return cmd
}
