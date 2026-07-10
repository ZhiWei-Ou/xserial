package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestConnWithoutPortPrintsHelp(t *testing.T) {
	cmd := NewConnCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want error")
	}
	if got := output.String(); !strings.Contains(got, "Usage:") || !strings.Contains(got, "conn <port> [baud]") {
		t.Fatalf("Execute() output = %q, want conn usage", got)
	}
}

func TestConnExposesCombinedSerialConfig(t *testing.T) {
	cmd := NewConnCommand()

	flag := cmd.Flags().Lookup("cfg")
	if flag == nil {
		t.Fatal("--cfg flag not found")
	}
	if flag.Shorthand != "c" {
		t.Fatalf("--cfg shorthand = %q, want c", flag.Shorthand)
	}
	if flag.DefValue != "8,N,1" {
		t.Fatalf("--cfg default = %q, want 8,N,1", flag.DefValue)
	}

	for _, name := range []string{"baud", "data-bits", "parity", "stop-bits"} {
		if flag := cmd.Flags().Lookup(name); flag != nil {
			t.Fatalf("legacy flag %q exists, want nil", name)
		}
	}
}

func TestConnDoesNotExposePortFlag(t *testing.T) {
	cmd := NewConnCommand()

	if flag := cmd.Flags().Lookup("port"); flag != nil {
		t.Fatalf("port flag exists, want nil")
	}
	if flag := cmd.Flags().ShorthandLookup("p"); flag != nil {
		t.Fatalf("-p shorthand exists, want nil")
	}
}

func TestParseConnOptionsUsesScreenStyleDefaults(t *testing.T) {
	opts, err := parseConnOptions([]string{"/dev/ttyUSB0"}, "8,N,1")
	if err != nil {
		t.Fatalf("parseConnOptions() error = %v", err)
	}

	if opts.port != "/dev/ttyUSB0" || opts.baud != 115200 || opts.dataBits != 8 || opts.parity != "none" || opts.stopBits != "1" {
		t.Fatalf("parseConnOptions() = %#v, want /dev/ttyUSB0 115200 8N1", opts)
	}
}

func TestParseConnOptionsAcceptsBaudAndLowercaseParity(t *testing.T) {
	opts, err := parseConnOptions([]string{"COM3", "9600"}, "7,e,2")
	if err != nil {
		t.Fatalf("parseConnOptions() error = %v", err)
	}

	if opts.baud != 9600 || opts.dataBits != 7 || opts.parity != "even" || opts.stopBits != "2" {
		t.Fatalf("parseConnOptions() = %#v, want 9600 7E2", opts)
	}
}

func TestParseConnOptionsRejectsInvalidConfig(t *testing.T) {
	for _, cfg := range []string{"8,N", "8,X,1", "8,N,3"} {
		t.Run(cfg, func(t *testing.T) {
			if _, err := parseConnOptions([]string{"COM3"}, cfg); err == nil {
				t.Fatalf("parseConnOptions(%q) error = nil, want error", cfg)
			}
		})
	}
}
