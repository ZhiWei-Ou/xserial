package cmd

import (
	"fmt"

	"github.com/ZhiWei-Ou/xserial/internal/serialport"
	"github.com/spf13/cobra"
)

func NewListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available serial ports",
		RunE: func(cmd *cobra.Command, args []string) error {
			ports, err := serialport.List()
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
		},
	}
}
