//go:build !windows

package replay

import "io"

func prepareOutput(io.Writer) (func() error, error) {
	return func() error { return nil }, nil
}
