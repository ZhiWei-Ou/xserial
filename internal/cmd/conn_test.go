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

func TestConnDoesNotExposeSerialConfigFlags(t *testing.T) {
	cmd := NewRootCommand()

	if flag := cmd.Flags().Lookup("cfg"); flag != nil {
		t.Fatal("--cfg flag exists, want nil")
	}
	if flag := cmd.Flags().ShorthandLookup("c"); flag != nil {
		t.Fatal("-c shorthand exists, want nil")
	}

	for _, name := range []string{"baud", "data-bits", "parity", "stop-bits"} {
		if flag := cmd.Flags().Lookup(name); flag != nil {
			t.Fatalf("legacy flag %q exists, want nil", name)
		}
	}
}

func TestConnExposesReceiveLog(t *testing.T) {
	cmd := NewRootCommand()

	flag := cmd.Flags().Lookup("log")
	if flag == nil {
		t.Fatal("--log flag not found")
	}
	if flag.DefValue != "" {
		t.Fatalf("--log default = %q, want empty", flag.DefValue)
	}

	timeFlag := cmd.Flags().Lookup("time")
	if timeFlag == nil {
		t.Fatal("--time flag not found")
	}
	if timeFlag.DefValue != "false" {
		t.Fatalf("--time default = %q, want false", timeFlag.DefValue)
	}
	if timeFlag.Shorthand != "t" {
		t.Fatalf("--time shorthand = %q, want t", timeFlag.Shorthand)
	}
	if flag := cmd.Flags().Lookup("log-time-format"); flag != nil {
		t.Fatal("legacy --log-time-format flag exists, want nil")
	}
	if flag := cmd.Flags().Lookup("TUI"); flag == nil {
		t.Fatal("--TUI flag not found")
	} else if flag.DefValue != "false" {
		t.Fatalf("--TUI default = %q, want false", flag.DefValue)
	}
	if flag := cmd.Flags().Lookup("reconnect"); flag != nil {
		t.Fatal("--reconnect flag should not exist")
	}
}

func TestConnTimeFlagUsesFixedFormat(t *testing.T) {
	for _, flag := range []string{"", "-t", "--time"} {
		t.Run(flag, func(t *testing.T) {
			var got connOptions
			cmd := newRootCommand(rootDependencies{conn: func(_ context.Context, opts connOptions) error { got = opts; return nil }})
			args := []string{"test-port"}
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
			if got.timeFormat != want {
				t.Fatalf("time format = %q, want %q", got.timeFormat, want)
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

func TestOpenReceiveLogWithoutPathReturnsNilWriter(t *testing.T) {
	logFile, err := openReceiveLog("")
	if err != nil {
		t.Fatalf("openReceiveLog() error = %v", err)
	}
	if logFile != nil {
		t.Fatalf("openReceiveLog() = %#v, want nil", logFile)
	}
}

func TestConnDoesNotExposePortFlag(t *testing.T) {
	cmd := NewRootCommand()

	if flag := cmd.Flags().Lookup("port"); flag != nil {
		t.Fatalf("port flag exists, want nil")
	}
	if flag := cmd.Flags().ShorthandLookup("p"); flag != nil {
		t.Fatalf("-p shorthand exists, want nil")
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
