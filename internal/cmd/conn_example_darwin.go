//go:build darwin

package cmd

import "strings"

const directConnExamplePort = "/dev/cu.usbserial-0001"

const connExamples = `  xserial conn /dev/cu.usbserial-0001
  xserial conn /dev/cu.usbserial-0001 9600 -c 8,N,1`

const directConnExamples = `  xserial /dev/cu.usbserial-0001
  xserial /dev/cu.usbserial-0001 9600 --tui`

func isSerialPortName(name string) bool {
	return len(name) > len("/dev/cu.") &&
		(strings.HasPrefix(name, "/dev/cu.") || strings.HasPrefix(name, "/dev/tty."))
}
