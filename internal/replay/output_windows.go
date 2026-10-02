//go:build windows

package replay

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

func prepareOutput(output io.Writer) (func() error, error) {
	file, ok := output.(*os.File)
	if !ok {
		return func() error { return nil }, nil
	}
	handle := windows.Handle(file.Fd())
	var original uint32
	if err := windows.GetConsoleMode(handle, &original); err != nil {
		return nil, fmt.Errorf("get console output mode: %w", err)
	}
	if err := windows.SetConsoleMode(handle, original|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING|windows.DISABLE_NEWLINE_AUTO_RETURN); err != nil {
		return nil, fmt.Errorf("enable native terminal output: %w", err)
	}
	return func() error {
		return windows.SetConsoleMode(handle, original)
	}, nil
}
