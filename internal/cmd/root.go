package cmd

import (
	"context"
	"fmt"
	"os"
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
		Example:       directConnExamples,
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
	rootCmd := NewRootCommand()
	rootCmd.SetArgs(resolveRootArgs(os.Args[1:]))
	return rootCmd.Execute()
}

func ExecuteContext(ctx context.Context) error {
	rootCmd := NewRootCommand()
	rootCmd.SetArgs(resolveRootArgs(os.Args[1:]))
	return rootCmd.ExecuteContext(ctx)
}

func resolveRootArgs(args []string) []string {
	if len(args) == 0 || !isSerialPortName(args[0]) {
		return args
	}
	resolved := make([]string, 0, len(args)+1)
	resolved = append(resolved, "conn")
	return append(resolved, args...)
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
