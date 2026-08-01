package cmd

import (
	"fmt"

	"github.com/ZhiWei-Ou/xserial/internal/serialport"
	"github.com/spf13/cobra"
)

func newListCommand(list func() ([]serialport.Info, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available serial ports",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, list)
		},
	}
}

func runList(cmd *cobra.Command, list func() ([]serialport.Info, error)) error {
	ports, err := list()
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
