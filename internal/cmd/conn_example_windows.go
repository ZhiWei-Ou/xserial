//go:build windows

package cmd

const directConnExamplePort = "COM3"

const directConnExamples = `  xserial COM3 9600,8,n,1
  xserial COM3 --tui`
