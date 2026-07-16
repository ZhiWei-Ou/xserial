//go:build !linux && !darwin && !windows

package cmd

const connExamples = `  xserial conn <port>
  xserial conn <port> 9600 -c 8,N,1`
