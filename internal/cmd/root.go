package cmd

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/ZhiWei-Ou/xserial/internal/serialport"
	"github.com/spf13/cobra"
)

const fallbackVersion = "v0.0.1"

var buildVersion string

const asciiPrefix = `
 __  __              _       _
 \ \/ /___  ___ _ __(_) __ _| |
  \  // __|/ _ \ '__| |/ _` + "`" + ` | |
  /  \\__ \  __/ |  | | (_| | |
 /_/\_\___/\___|_|  |_|\__,_|_| %s
`

type rootDependencies struct {
	list func() ([]serialport.Info, error)
	conn func(context.Context, connOptions) error
}

func NewRootCommand() *cobra.Command {
	return newRootCommand(rootDependencies{list: serialport.List, conn: runConn})
}

func newRootCommand(deps rootDependencies) *cobra.Command {
	version := currentVersion()
	var flags connFlags

	rootCmd := &cobra.Command{
		Use:           "xserial [port] [cfg]",
		Short:         "Cross-platform serial terminal",
		Example:       directConnExamples,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			opts, err := parseConnOptions(args, flags.logPath, flags.timeFormat, flags.useTUI)
			if err != nil {
				return err
			}
			opts.hexdump = flags.hexdump
			return deps.conn(cmd.Context(), opts)
		},
	}

	rootCmd.SetHelpTemplate(fmt.Sprintf(asciiPrefix, version) + "\n" + rootCmd.HelpTemplate())
	bindConnFlags(rootCmd, &flags)
	rootCmd.AddCommand(newListCommand(deps.list))
	rootCmd.AddCommand(newVersionCommand(version))
	return rootCmd
}

func newVersionCommand(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version)
		},
	}
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
