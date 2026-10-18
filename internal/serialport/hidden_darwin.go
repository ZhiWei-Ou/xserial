package serialport

import "strings"

// macOS exposes every serial device twice; /dev/cu.* is the callout node for
// initiating connections, while /dev/tty.* may block waiting for carrier.
// Bluetooth-Incoming-Port and debug-console exist on hosts with no device attached.
func isHiddenPort(name string) bool {
	if strings.HasPrefix(name, "/dev/tty.") {
		return true
	}
	switch name {
	case "/dev/cu.Bluetooth-Incoming-Port", "/dev/cu.debug-console":
		return true
	}
	return false
}
