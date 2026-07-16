//go:build !linux && !darwin && !windows

package cmd

const directConnExamplePort = "<port>"

const connExamples = `  xserial conn <port>
  xserial conn <port> 9600 -c 8,N,1`

const directConnExamples = ""

func isSerialPortName(string) bool { return false }
