package cmd

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestConnShowsOnlyCurrentPlatformExamples(t *testing.T) {
	cmd := NewRootCommand()

	examples := map[string]string{
		"linux":   "/dev/ttyUSB0",
		"darwin":  "/dev/cu.usbserial-0001",
		"windows": "COM3",
	}
	want, supported := examples[runtime.GOOS]
	if !supported {
		want = "<port>"
	}
	wantExample := "xserial " + want + " 9600,8,n,1"
	if !strings.Contains(cmd.Example, wantExample) {
		t.Fatalf("Example = %q, want %q", cmd.Example, wantExample)
	}
	wantTUIExample := "xserial " + want + " --TUI"
	if !strings.Contains(cmd.Example, wantTUIExample) {
		t.Fatalf("Example = %q, want %q", cmd.Example, wantTUIExample)
	}
	for platform, port := range examples {
		if platform != runtime.GOOS && strings.Contains(cmd.Example, port) {
			t.Fatalf("Example = %q, unexpectedly contains %s port %q", cmd.Example, platform, port)
		}
	}
}

func TestConnReceiveLogAndTimeFlagsReachConnection(t *testing.T) {
	for _, flag := range []string{"", "-t", "--time"} {
		t.Run(flag, func(t *testing.T) {
			var got connOptions
			cmd := newRootCommand(rootDependencies{conn: func(_ context.Context, opts connOptions) error { got = opts; return nil }})
			args := []string{"test-port", "--log", "device.log"}
			if flag != "" {
				args = append(args, flag)
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			want := ""
			if flag != "" {
				want = defaultReceiveTimeFormat
			}
			if got.logPath != "device.log" || got.timeFormat != want {
				t.Fatalf("log path = %q, time format = %q; want device.log, %q", got.logPath, got.timeFormat, want)
			}
		})
	}
}

func TestConnTimeFlagRejectsCustomFormat(t *testing.T) {
	for _, flag := range []string{"--time=2006-01-02 15:04:05", "-t=15:04:05"} {
		cmd := NewRootCommand()
		if err := cmd.ParseFlags([]string{flag}); err == nil {
			t.Fatalf("ParseFlags(%q) succeeded", flag)
		}
	}
}

func TestParseConnOptionsUsesScreenStyleDefaults(t *testing.T) {
	opts, err := parseConnOptions([]string{"/dev/ttyUSB0"}, "", false, false)
	if err != nil {
		t.Fatalf("parseConnOptions() error = %v", err)
	}

	if opts.port != "/dev/ttyUSB0" || opts.baud != 115200 || opts.dataBits != 8 || opts.parity != "none" || opts.stopBits != "1" {
		t.Fatalf("parseConnOptions() = %#v, want /dev/ttyUSB0 115200 8N1", opts)
	}
}

func TestParseConnOptionsAcceptsPartialPositionalConfig(t *testing.T) {
	tests := []struct {
		cfg      string
		baud     int
		dataBits int
		parity   string
		stopBits string
	}{
		{cfg: "9600", baud: 9600, dataBits: 8, parity: "none", stopBits: "1"},
		{cfg: "9600,8", baud: 9600, dataBits: 8, parity: "none", stopBits: "1"},
		{cfg: "9600,8,n", baud: 9600, dataBits: 8, parity: "none", stopBits: "1"},
		{cfg: "9600,8,n,1", baud: 9600, dataBits: 8, parity: "none", stopBits: "1"},
		{cfg: "57600,7,e,2", baud: 57600, dataBits: 7, parity: "even", stopBits: "2"},
	}

	for _, tt := range tests {
		t.Run(tt.cfg, func(t *testing.T) {
			opts, err := parseConnOptions([]string{"COM3", tt.cfg}, "capture.log", true, true)
			if err != nil {
				t.Fatalf("parseConnOptions() error = %v", err)
			}
			if opts.baud != tt.baud || opts.dataBits != tt.dataBits || opts.parity != tt.parity || opts.stopBits != tt.stopBits {
				t.Fatalf("parseConnOptions() = %#v", opts)
			}
			if opts.logPath != "capture.log" || opts.timeFormat != defaultReceiveTimeFormat || !opts.tui {
				t.Fatalf("parseConnOptions() = %#v, want log, time, and TUI options preserved", opts)
			}
		})
	}
}

func TestParseConnOptionsRejectsInvalidConfig(t *testing.T) {
	for _, cfg := range []string{"", "0", "baud", "9600,", "9600,0", "9600,8,X", "9600,8,N,3", "9600,8,N,1,extra"} {
		t.Run(cfg, func(t *testing.T) {
			if _, err := parseConnOptions([]string{"COM3", cfg}, "", false, false); err == nil {
				t.Fatalf("parseConnOptions(%q) error = nil, want error", cfg)
			}
		})
	}
}
