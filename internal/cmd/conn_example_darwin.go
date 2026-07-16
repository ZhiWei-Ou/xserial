//go:build darwin

package cmd

const connExamples = `  xserial conn /dev/tty.usbserial-0001
  xserial conn /dev/tty.usbserial-0001 9600 -c 8,N,1`
