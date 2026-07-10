package cmd

import (
	"github.com/spf13/cobra"
)

const asciiPrefix = `
 __  __ ____               _         _
 \ \/ // ___|   ___  _ __ (_)  __ _ | |
  \  / \___ \  / _ \| '__|| | / _` + "`" + ` || |
  /  \  ___) ||  __/| |   | || (_| || |
 /_/\_\|____/  \___||_|   |_| \__,_||_| v1.0
`

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:           "xserial",
		Short:         "Cross-platform serial terminal",
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.SetHelpTemplate(
		asciiPrefix + "\n" + rootCmd.HelpTemplate(),
	)

	rootCmd.AddCommand(NewListCommand())
	rootCmd.AddCommand(NewConnCommand())
	return rootCmd
}

func Execute() error {
	return NewRootCommand().Execute()
}
