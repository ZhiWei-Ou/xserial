//go:build linux || windows || (darwin && cgo)

package serialport

import "go.bug.st/serial/enumerator"

func detailedPorts() map[string]Info {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil
	}

	details := make(map[string]Info, len(ports))
	for _, port := range ports {
		details[port.Name] = Info{
			Name:         port.Name,
			IsUSB:        port.IsUSB,
			VID:          port.VID,
			PID:          port.PID,
			SerialNumber: port.SerialNumber,
			Product:      port.Product,
		}
	}
	return details
}
