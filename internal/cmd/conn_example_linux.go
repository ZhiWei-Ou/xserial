//go:build linux

package cmd

const connExamples = `  xserial conn /dev/ttyUSB0
  xserial conn /dev/ttyUSB0 9600 -c 8,N,1`
