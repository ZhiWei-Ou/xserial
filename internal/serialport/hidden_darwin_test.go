package serialport

import "testing"

func TestIsHiddenPortDarwin(t *testing.T) {
	tests := map[string]bool{
		"/dev/cu.usbserial-110":            false,
		"/dev/cu.usbmodem1101":             false,
		"/dev/tty.usbserial-110":           true,
		"/dev/tty.Bluetooth-Incoming-Port": true,
		"/dev/tty.debug-console":           true,
		"/dev/cu.Bluetooth-Incoming-Port":  true,
		"/dev/cu.debug-console":            true,
	}
	for name, want := range tests {
		if got := isHiddenPort(name); got != want {
			t.Errorf("isHiddenPort(%q) = %v, want %v", name, got, want)
		}
	}
}
