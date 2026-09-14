package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
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

func TestRootWithoutPositionalsShowsHelp(t *testing.T) {
	listed := false
	connected := false
	cmd := newRootCommand(rootDependencies{
		list: func() ([]serialport.Info, error) {
			listed = true
			return nil, nil
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
	if listed || connected {
		t.Fatalf("listed=%v connected=%v", listed, connected)
	}
	if !strings.Contains(output.String(), "Usage:") || !strings.Contains(output.String(), "list") {
		t.Fatalf("help output = %q", output.String())
	}
}

func TestListCommandListsPorts(t *testing.T) {
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
	cmd.SetArgs([]string{"list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if connected || output.String() != "/dev/test0\n" {
		t.Fatalf("connected=%v output=%q", connected, output.String())
	}
}

func TestRootConnectsFromPositionalConfig(t *testing.T) {
	var got connOptions
	cmd := newRootCommand(rootDependencies{
		list: func() ([]serialport.Info, error) { return nil, nil },
		conn: func(_ context.Context, opts connOptions) error {
			got = opts
			return nil
		},
	})
	cmd.SetArgs([]string{"custom-port", "9600,7,e,2", "--TUI"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got.port != "custom-port" || got.baud != 9600 || got.dataBits != 7 || got.parity != "even" || got.stopBits != "2" || !got.tui {
		t.Fatalf("connection options = %#v", got)
	}
}

func TestUnrecognizedCommandWordsAreOrdinaryPortNames(t *testing.T) {
	for _, port := range []string{"conn", "lsit"} {
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

func TestRootHelpRoutes(t *testing.T) {
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
}

func TestVersionCommandShowsShortVersion(t *testing.T) {
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.TrimSpace(output.String()) != currentVersion() {
		t.Fatalf("version output = %q", output.String())
	}

}

func TestRootHelpShowsCompactXserialLogo(t *testing.T) {
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output.String(), `\ \/ /___  ___ _ __(_) __ _| |`) {
		t.Fatalf("help output does not contain compact Xserial logo: %q", output.String())
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

func TestRootHexdumpFlags(t *testing.T) {
	for _, args := range [][]string{{"-h", "test-port"}, {"test-port", "--hexdump", "--time"}, {"test-port"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var got connOptions
			cmd := newRootCommand(rootDependencies{conn: func(_ context.Context, opts connOptions) error {
				got = opts
				return nil
			}})
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if got.port != "test-port" || got.hexdump != (len(args) > 1) {
				t.Fatalf("connection options = %#v", got)
			}
		})
	}
}

func TestRootRejectsHexdumpWithTUI(t *testing.T) {
	cmd := newRootCommand(rootDependencies{conn: func(context.Context, connOptions) error {
		t.Fatal("connection should not be opened")
		return nil
	}})
	cmd.SetArgs([]string{"test-port", "-h", "--TUI"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "hexdump") {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestVersionFlagsShowBuildInfoWithoutConnecting(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("test binary has no build info")
	}
	for _, args := range [][]string{{"-v"}, {"--version"}, {"test-port", "-v"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cmd := newRootCommand(rootDependencies{conn: func(context.Context, connOptions) error { t.Fatal("version must not open a port"); return nil }})
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "Go:          "+info.GoVersion) || strings.Contains(output.String(), "dep\t") {
				t.Fatalf("version output = %q", output.String())
			}
		})
	}
}

func TestDetailedVersionPreservesBuildMetadata(t *testing.T) {
	info := &debug.BuildInfo{GoVersion: "go1.26.2", Path: "example.com/app/cmd/app", Main: debug.Module{Path: "example.com/app", Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "GOOS", Value: "linux"}, {Key: "GOARCH", Value: "arm64"}, {Key: "vcs.time", Value: "2026-09-14T13:47:29Z"}, {Key: "vcs.revision", Value: "abcdef"}, {Key: "vcs.modified", Value: "true"}}}
	var output bytes.Buffer
	if err := writeBuildInfo(&output, info, true); err != nil {
		t.Fatal(err)
	}
	want := "Version:     (devel)\nGo:          go1.26.2\nPlatform:    linux/arm64\nCommit:      abcdef\nCommit time: 2026-09-14T13:47:29Z\nModified:    true\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
	var minimal bytes.Buffer
	if err := writeBuildInfo(&minimal, &debug.BuildInfo{GoVersion: "go1.26.2"}, true); err != nil {
		t.Fatal(err)
	}
	if minimal.String() != "Go:          go1.26.2\n" {
		t.Fatalf("minimal output = %q", minimal.String())
	}

	if err := writeBuildInfo(&output, nil, false); err == nil {
		t.Fatal("missing build info should return an error")
	}
	if err := writeBuildInfo(versionErrorWriter{}, info, true); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write error = %v", err)
	}
}

type versionErrorWriter struct{}

func (versionErrorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
