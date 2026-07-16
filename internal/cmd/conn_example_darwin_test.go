//go:build darwin

package cmd

import "testing"

func TestDarwinSerialPortNames(t *testing.T) {
	testSerialPortNames(t,
		[]string{"/dev/cu.usbserial-0001", "/dev/cu.usbmodem1234", "/dev/tty.usbserial-0001"},
		[]string{"/dev/cu.", "/dev/tty", "/dev/ttyUSB0", "COM3", "lsit"},
	)
}
