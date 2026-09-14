//go:build darwin

package cmd

const directConnExamplePort = "/dev/cu.usbserial-0001"

const directConnExamples = `  xserial /dev/cu.usbserial-0001 9600,8,n,1
  xserial /dev/cu.usbserial-0001 --TUI`
