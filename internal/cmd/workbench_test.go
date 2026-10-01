package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
)

func TestWorkbenchFlagsAreValidatedBeforeOpeningPort(t *testing.T) {
	for _, args := range [][]string{{"test", "--workbench", "--frame", "fixed:0"}, {"test", "--frame", "fixed:9"}, {"test", "--workbench", "--TUI"}, {"test", "--workbench", "--hexdump"}} {
		cmd := newRootCommand(rootDependencies{conn: func(context.Context, connOptions) error { t.Fatal("invalid configuration opened port"); return nil }})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	var got connOptions
	cmd := newRootCommand(rootDependencies{conn: func(_ context.Context, opts connOptions) error { got = opts; return nil }})
	cmd.SetArgs([]string{"test", "9600", "--workbench", "--frame", "modbus-read", "--record", "test.xsr"})
	if err := cmd.Execute(); err != nil || !got.workbench || got.framing.Kind != "modbus-read" || got.recordPath != "test.xsr" {
		t.Fatalf("options=%+v, %v", got, err)
	}
}

func TestRecordingAndExportNeverOverwriteExistingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.xsr")
	file, writer, err := openRecording(path, middleware.ConnectionConfig{PortName: "test"})
	if err != nil {
		t.Fatal(err)
	}
	writer.Append(capture.Record{Kind: "rx", Data: []byte{0xAA, 0, 0xFF}})
	file.Close()
	if _, _, err := openRecording(path, middleware.ConnectionConfig{}); err == nil {
		t.Fatal("recording overwritten")
	}
	cmd := newExportCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{path, "--match", "AA00"})
	if err := cmd.Execute(); err != nil || !bytes.Contains(output.Bytes(), []byte("AA 00 FF")) {
		t.Fatalf("export=%q, err=%v", output.String(), err)
	}
	destination := filepath.Join(t.TempDir(), "out.txt")
	os.WriteFile(destination, []byte("keep"), 0o600)
	cmd = newExportCommand()
	cmd.SetArgs([]string{path, "-o", destination})
	if err := cmd.Execute(); err == nil {
		t.Fatal("export overwritten")
	}
	data, _ := os.ReadFile(destination)
	if string(data) != "keep" {
		t.Fatal("existing export changed")
	}
}
