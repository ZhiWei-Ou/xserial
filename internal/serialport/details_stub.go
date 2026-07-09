//go:build !(linux || windows || (darwin && cgo))

package serialport

func detailedPorts() map[string]Info {
	return nil
}
