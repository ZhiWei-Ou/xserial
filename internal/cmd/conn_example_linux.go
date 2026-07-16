//go:build linux

package cmd

import "regexp"

const directConnExamplePort = "/dev/ttyUSB0"

const connExamples = `  xserial conn /dev/ttyUSB0
  xserial conn /dev/ttyUSB0 9600 -c 8,N,1`

const directConnExamples = `  xserial /dev/ttyUSB0
  xserial /dev/ttyUSB0 9600 --tui`

var serialPortNamePattern = regexp.MustCompile(`^/dev/(?:(?:ttyS|ttyHS|ttyUSB|ttyACM|ttyAMA|rfcomm|ttyO|ttymxc)[0-9]{1,3}|serial/(?:by-id|by-path)/.+)$`)

func isSerialPortName(name string) bool {
	return serialPortNamePattern.MatchString(name)
}
