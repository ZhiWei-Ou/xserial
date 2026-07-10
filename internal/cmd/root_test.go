package cmd

import (
	"bytes"
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
