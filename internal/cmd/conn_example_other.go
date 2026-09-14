//go:build !linux && !darwin && !windows

package cmd

const directConnExamplePort = "<port>"

const directConnExamples = `  xserial <port> 9600,8,n,1
  xserial <port> --TUI`
