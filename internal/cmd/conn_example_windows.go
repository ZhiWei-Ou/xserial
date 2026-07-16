//go:build windows

package cmd

import "regexp"

const directConnExamplePort = "COM3"

const connExamples = `  xserial conn COM3
  xserial conn COM3 9600 -c 8,N,1`

const directConnExamples = `  xserial COM3
  xserial COM3 9600 --tui`

var serialPortNamePattern = regexp.MustCompile(`(?i)^(?:\\\\\.\\)?COM[0-9]+$`)

func isSerialPortName(name string) bool {
	return serialPortNamePattern.MatchString(name)
}
