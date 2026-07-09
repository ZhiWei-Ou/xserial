package session

import (
	"bytes"
	"testing"
)

func TestPrefixMachinePassesThroughInput(t *testing.T) {
	var serial bytes.Buffer
	machine := NewPrefixMachine(DefaultPrefixKey)

	for _, b := range []byte("help\r") {
		action, err := machine.HandleByte(b, &serial)
		if err != nil {
			t.Fatalf("HandleByte() error = %v", err)
		}
		if action != ActionNone {
			t.Fatalf("HandleByte() action = %v, want %v", action, ActionNone)
		}
	}

	if got := serial.String(); got != "help\r" {
		t.Fatalf("serial output = %q, want %q", got, "help\r")
	}
}

func TestPrefixMachineRecognizesHelpAndQuit(t *testing.T) {
	tests := []struct {
		name string
		key  byte
		want PrefixAction
	}{
		{name: "help", key: 'h', want: ActionHelp},
		{name: "upload", key: 'u', want: ActionUpload},
		{name: "quit", key: 'q', want: ActionQuit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var serial bytes.Buffer
			machine := NewPrefixMachine(DefaultPrefixKey)

			if action, err := machine.HandleByte(DefaultPrefixKey, &serial); err != nil || action != ActionNone {
				t.Fatalf("prefix HandleByte() = %v, %v; want ActionNone, nil", action, err)
			}

			action, err := machine.HandleByte(tt.key, &serial)
			if err != nil {
				t.Fatalf("HandleByte() error = %v", err)
			}
			if action != tt.want {
				t.Fatalf("HandleByte() action = %v, want %v", action, tt.want)
			}
			if serial.Len() != 0 {
				t.Fatalf("serial output = %q, want empty", serial.String())
			}
		})
	}
}

func TestPrefixMachineWritesLiteralPrefix(t *testing.T) {
	var serial bytes.Buffer
	machine := NewPrefixMachine(DefaultPrefixKey)

	if _, err := machine.HandleByte(DefaultPrefixKey, &serial); err != nil {
		t.Fatalf("prefix HandleByte() error = %v", err)
	}
	action, err := machine.HandleByte(DefaultPrefixKey, &serial)
	if err != nil {
		t.Fatalf("HandleByte() error = %v", err)
	}
	if action != ActionWroteLiteralPrefix {
		t.Fatalf("HandleByte() action = %v, want %v", action, ActionWroteLiteralPrefix)
	}
	if got := serial.Bytes(); !bytes.Equal(got, []byte{DefaultPrefixKey}) {
		t.Fatalf("serial output = %v, want [%d]", got, DefaultPrefixKey)
	}
}
