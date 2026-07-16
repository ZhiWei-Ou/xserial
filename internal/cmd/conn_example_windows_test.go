//go:build windows

package cmd

import "testing"

func TestWindowsSerialPortNames(t *testing.T) {
	testSerialPortNames(t,
		[]string{"COM3", "com12", `\\.\COM10`},
		[]string{"COM", "COMx", "COM3x", "/dev/ttyUSB0", "lsit"},
	)
}
