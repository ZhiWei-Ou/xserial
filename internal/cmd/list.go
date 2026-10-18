package cmd

import (
	"fmt"

	"github.com/ZhiWei-Ou/xserial/internal/serialport"
	"github.com/spf13/cobra"
)

func newListCommand(list func(all bool) ([]serialport.Info, error)) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available serial ports",
		Long: `List available serial ports.

On macOS, system ports (Bluetooth-Incoming-Port, debug-console) and the
/dev/tty.* call-in duplicates of /dev/cu.* devices are hidden by default.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, list, all)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "show every port, including hidden system ports")
	return cmd
}

func runList(cmd *cobra.Command, list func(all bool) ([]serialport.Info, error), all bool) error {
	ports, err := list(all)
	if err != nil {
		return err
	}
	if len(ports) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no serial ports found")
		return nil
	}
	for _, port := range ports {
		fmt.Fprintln(cmd.OutOrStdout(), serialport.FormatInfo(port))
	}
	return nil
}
