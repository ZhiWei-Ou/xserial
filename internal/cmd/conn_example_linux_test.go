//go:build linux

package cmd

import "testing"

func TestLinuxSerialPortNames(t *testing.T) {
	testSerialPortNames(t,
		[]string{"/dev/ttyUSB0", "/dev/ttyACM12", "/dev/serial/by-id/usb-device", "/dev/serial/by-path/pci-device"},
		[]string{"/dev/ttyUSB", "/dev/tty.usbserial-0001", "/dev/null", "COM3", "lsit"},
	)
}
