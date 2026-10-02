package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/capture"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
)

func TestReplayDefaultsToNativeTerminalAndHexdumpPreservesBothDirections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shell.xsr")
	file, writer, err := openRecording(path, middleware.ConnectionConfig{PortName: "COM5"})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []capture.Record{
		{Kind: "tx", Data: []byte("ver\r")},
		{Kind: "rx", Data: []byte("\x1b[Kxsh > ver\r\nversion: 1\r\nxsh > ")},
		{Kind: "closed"},
	} {
		if _, err := writer.Append(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, hexdump := range []bool{false, true} {
		cmd := newRootCommand(rootDependencies{conn: func(context.Context, connOptions) error {
			t.Fatal("offline replay tried to connect to a device")
			return nil
		}})
		var output bytes.Buffer
		cmd.SetIn(strings.NewReader(""))
		cmd.SetOut(&output)
		args := []string{"replay", path}
		if hexdump {
			args = append(args, "--hexdump")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if hexdump {
			if !strings.Contains(output.String(), "TX") || !strings.Contains(output.String(), "RX") || !strings.Contains(output.String(), "76 65 72 0D") {
				t.Fatalf("HEX replay = %q", output.String())
			}
		} else if output.String() != "\x1b[Kxsh > ver\r\nversion: 1\r\nxsh > " {
			t.Fatalf("native output changed or TX was duplicated: %q", output.String())
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("replay changed the recording: %v", err)
	}
}

func TestReplayFrameOptionsAreValidatedBeforeReadingTheFile(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"missing.xsr", "--frame", "fixed:8"}, "requires --hexdump"},
		{[]string{"missing.xsr", "--hexdump", "--frame", "gap:0ms"}, "positive duration"},
		{[]string{"missing.xsr", "--hexdump", "--frame", "gap:-1s"}, "positive duration"},
		{[]string{"missing.xsr", "--hexdump", "--frame", "gap:invalid"}, "positive duration"},
		{[]string{"missing.xsr", "--hexdump", "--frame", "fixed:0"}, "invalid frame rule"},
	} {
		cmd := newReplayCommand()
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: error %v, want %q", tc.args, err, tc.want)
		}
	}
}
