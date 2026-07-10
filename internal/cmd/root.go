package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

const fallbackVersion = "v0.0.1"

const asciiPrefix = `
 __  __ ____               _         _
 \ \/ // ___|   ___  _ __ (_)  __ _ | |
  \  / \___ \  / _ \| '__|| | / _` + "`" + ` || |
  /  \  ___) ||  __/| |   | || (_| || |
 /_/\_\|____/  \___||_|   |_| \__,_||_| %s
`

func NewRootCommand() *cobra.Command {
	version := currentVersion()
	rootCmd := &cobra.Command{
		Use:           "xserial",
		Short:         "Cross-platform serial terminal",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	rootCmd.SetHelpTemplate(
		fmt.Sprintf(asciiPrefix, version) + "\n" + rootCmd.HelpTemplate(),
	)

	rootCmd.AddCommand(NewListCommand())
	rootCmd.AddCommand(NewConnCommand())
	return rootCmd
}

func Execute() error {
	return NewRootCommand().Execute()
}

func currentVersion() string {
	return versionFromBuildInfo(debug.ReadBuildInfo())
}

func versionFromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return fallbackVersion
	}
	return info.Main.Version
}
