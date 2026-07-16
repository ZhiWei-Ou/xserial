package cmd

import (
	"bytes"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionUsesModuleVersion(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
	}

	if got := versionFromBuildInfo(info, true); got != "v1.2.3" {
		t.Fatalf("versionFromBuildInfo() = %q, want v1.2.3", got)
	}
}

func TestVersionFallsBackForDevelopmentBuild(t *testing.T) {
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
	}

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

func TestRootCommandDisplaysVersion(t *testing.T) {
	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)

	if cmd.Version != currentVersion() {
		t.Fatalf("command version = %q, want %q", cmd.Version, currentVersion())
	}
	if err := cmd.Help(); err != nil {
		t.Fatalf("Help() error = %v", err)
	}
	if got := output.String(); !strings.Contains(got, currentVersion()) {
		t.Fatalf("help output does not contain version %q", currentVersion())
	}
}

func TestRootDefaultsPlatformSerialPortToConn(t *testing.T) {
	if directConnExamples == "" {
		t.Skip("direct conn is not supported on this platform")
	}
	args := []string{directConnExamplePort, "9600", "--tui"}
	want := []string{"conn", directConnExamplePort, "9600", "--tui"}

	if got := resolveRootArgs(args); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveRootArgs() = %v, want %v", got, want)
	}
}

func TestRootKeepsSubcommandsAndUnknownCommandsUnchanged(t *testing.T) {
	for _, args := range [][]string{{"list"}, {"conn", directConnExamplePort}, {"lsit"}, {"--help"}} {
		if got := resolveRootArgs(args); !reflect.DeepEqual(got, args) {
			t.Fatalf("resolveRootArgs(%v) = %v, want unchanged", args, got)
		}
	}
}

func TestRootRejectsUnknownCommand(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"lsit"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("Execute() error = %v, want unknown command", err)
	}
}

func TestRootHelpShowsDirectConnectionExample(t *testing.T) {
	if directConnExamples == "" {
		t.Skip("direct conn is not supported on this platform")
	}
	cmd := NewRootCommand()
	if !strings.Contains(cmd.Example, "xserial "+directConnExamplePort) {
		t.Fatalf("Example = %q, want direct port %q", cmd.Example, directConnExamplePort)
	}
}

func testSerialPortNames(t *testing.T, valid, invalid []string) {
	t.Helper()
	for _, name := range valid {
		if !isSerialPortName(name) {
			t.Errorf("isSerialPortName(%q) = false, want true", name)
		}
	}
	for _, name := range invalid {
		if isSerialPortName(name) {
			t.Errorf("isSerialPortName(%q) = true, want false", name)
		}
	}
}
