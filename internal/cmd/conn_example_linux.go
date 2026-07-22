//go:build linux

package cmd

const directConnExamplePort = "/dev/ttyUSB0"

const directConnExamples = `  xserial /dev/ttyUSB0
  xserial /dev/ttyUSB0 9600 --tui`
