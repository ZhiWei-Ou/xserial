//go:build linux

package cmd

const directConnExamplePort = "/dev/ttyUSB0"

const directConnExamples = `  xserial /dev/ttyUSB0 9600,8,n,1
  xserial /dev/ttyUSB0 --TUI`
