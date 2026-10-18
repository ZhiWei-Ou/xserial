//go:build !darwin

package serialport

func isHiddenPort(string) bool { return false }
