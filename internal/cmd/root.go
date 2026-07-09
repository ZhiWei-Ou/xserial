package cmd

import (
	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "xserial",
		Short:         "Cross-platform serial terminal",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.AddCommand(NewListCommand())
	rootCmd.AddCommand(NewConnCommand())
	return rootCmd
}

func Execute() error {
	return NewRootCommand().Execute()
}
