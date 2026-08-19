//go:build !windows

package serialport

import "go.bug.st/serial"

func configureReadCancellation(serial.Port) error { return nil }
