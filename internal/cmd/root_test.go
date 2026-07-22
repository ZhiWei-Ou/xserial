package cmd

import (
	"bytes"
	"context"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/serialport"
)

func TestVersionUsesModuleVersion(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}
	if got := versionFromBuildInfo(info, true); got != "v1.2.3" {
		t.Fatalf("versionFromBuildInfo() = %q, want v1.2.3", got)
	}
}

func TestVersionFallsBackForDevelopmentBuild(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
	if got := versionFromBuildInfo(info, true); got != fallbackVersion {
		t.Fatalf("versionFromBuildInfo() = %q, want %q", got, fallbackVersion)
	}
}

func TestVersionUsesReleaseBuildValue(t *testing.T) {
	previous := buildVersion
	buildVersion = "v1.2.3"
	t.Cleanup(func() { buildVersion = previous })
	if got := currentVersion(); got != "v1.2.3" {
		t.Fatalf("currentVersion() = %q, want v1.2.3", got)
	}
}

func TestRootWithoutPositionalsListsPorts(t *testing.T) {
	connected := false
	cmd := newRootCommand(rootDependencies{
		list: func() ([]serialport.Info, error) {
			return []serialport.Info{{Name: "/dev/test0"}}, nil
		},
		conn: func(context.Context, connOptions) error {
			connected = true
			return nil
		},
	})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if connected || output.String() != "/dev/test0\n" {
		t.Fatalf("connected=%v output=%q", connected, output.String())
	}
}

func TestRootTreatsAnyPositionalAsPort(t *testing.T) {
	var got connOptions
	cmd := newRootCommand(rootDependencies{
		list: func() ([]serialport.Info, error) { return nil, nil },
		conn: func(_ context.Context, opts connOptions) error {
			got = opts
			return nil
		},
	})
	cmd.SetArgs([]string{"custom-port", "9600", "--tui"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.port != "custom-port" || got.baud != 9600 || !got.tui {
		t.Fatalf("connection options = %#v", got)
	}
}

func TestRemovedCommandWordsAreOrdinaryPortNames(t *testing.T) {
	for _, port := range []string{"conn", "list", "lsit"} {
		t.Run(port, func(t *testing.T) {
			var got string
			cmd := newRootCommand(rootDependencies{
				list: func() ([]serialport.Info, error) { return nil, nil },
				conn: func(_ context.Context, opts connOptions) error { got = opts.port; return nil },
			})
			cmd.SetArgs([]string{port})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got != port {
				t.Fatalf("port = %q, want %q", got, port)
			}
		})
	}
}

func TestRootHelpAndVersionRoutes(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cmd := NewRootCommand()
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if !strings.Contains(output.String(), "Usage:") {
				t.Fatalf("help output = %q", output.String())
			}
		})
	}

	for _, args := range [][]string{{"-v"}, {"--version"}, {"version"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cmd := NewRootCommand()
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if strings.TrimSpace(output.String()) != currentVersion() {
				t.Fatalf("version output = %q", output.String())
			}
		})
	}
}

func TestRootHelpShowsDirectConnectionExample(t *testing.T) {
	if directConnExamples == "" {
		t.Skip("direct connection example is not available on this platform")
	}
	cmd := NewRootCommand()
	if !strings.Contains(cmd.Example, "xserial "+directConnExamplePort) {
		t.Fatalf("Example = %q, want direct port %q", cmd.Example, directConnExamplePort)
	}
}
