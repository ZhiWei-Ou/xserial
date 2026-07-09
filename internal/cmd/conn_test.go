package cmd

import (
	"bytes"
	"testing"
)

func TestConnRequiresPort(t *testing.T) {
	cmd := NewConnCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute() error = nil, want error")
	}
}

func TestConnDefaultFlags(t *testing.T) {
	cmd := NewConnCommand()

	tests := []struct {
		name string
		want string
	}{
		{name: "baud", want: "115200"},
		{name: "data-bits", want: "8"},
		{name: "parity", want: "none"},
		{name: "stop-bits", want: "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag := cmd.Flags().Lookup(tt.name)
			if flag == nil {
				t.Fatalf("flag %q not found", tt.name)
			}
			if flag.DefValue != tt.want {
				t.Fatalf("flag %q default = %q, want %q", tt.name, flag.DefValue, tt.want)
			}
		})
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
