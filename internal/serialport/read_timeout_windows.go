//go:build windows

package serialport

import (
	"time"

	"go.bug.st/serial"
)

const windowsReadTimeout = 100 * time.Millisecond

// Windows serial drivers do not consistently unblock an overlapped Read when
// another goroutine closes the handle. A bounded read lets the session observe
// cancellation without depending on that driver behavior.
func configureReadCancellation(port serial.Port) error {
	return port.SetReadTimeout(windowsReadTimeout)
}
