//go:build !linux && !darwin && !windows

package cmd

const directConnExamplePort = "<port>"

const directConnExamples = `  xserial <port>
  xserial <port> 9600 --tui`
