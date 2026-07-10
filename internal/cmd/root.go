package cmd

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

const fallbackVersion = "v0.0.1"

var buildVersion string

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

func ExecuteContext(ctx context.Context) error {
	return NewRootCommand().ExecuteContext(ctx)
}

func currentVersion() string {
	if buildVersion != "" {
		return buildVersion
	}
	return versionFromBuildInfo(debug.ReadBuildInfo())
}

func versionFromBuildInfo(info *debug.BuildInfo, ok bool) string {
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return fallbackVersion
	}
	return info.Main.Version
}
