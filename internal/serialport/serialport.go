package serialport

import (
	"fmt"
	"io"
	"strings"

	"go.bug.st/serial"
)

type Port interface {
	io.ReadWriteCloser
}

type Config struct {
	BaudRate int
	DataBits int
	Parity   string
	StopBits string
}

type Info struct {
	Name         string
	IsUSB        bool
	VID          string
	PID          string
	SerialNumber string
	Product      string
}

func List() ([]Info, error) {
	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}

	details := detailedPorts()
	infos := make([]Info, 0, len(ports))
	for _, port := range ports {
		if info, ok := details[port]; ok {
			infos = append(infos, info)
			continue
		}
		infos = append(infos, Info{Name: port})
	}
	return infos, nil
}

func FormatInfo(info Info) string {
	if !info.IsUSB {
		return info.Name
	}

	fields := []string{info.Name}
	if info.VID != "" {
		fields = append(fields, "vid="+info.VID)
	}
	if info.PID != "" {
		fields = append(fields, "pid="+info.PID)
	}
	if info.SerialNumber != "" {
		fields = append(fields, "serial="+info.SerialNumber)
	}
	if info.Product != "" {
		fields = append(fields, "product="+info.Product)
	}
	return strings.Join(fields, " ")
}

func Open(name string, cfg Config) (Port, error) {
	mode, err := modeFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	port, err := serial.Open(name, mode)
	if err != nil {
		return nil, err
	}
	if err := configureReadCancellation(port); err != nil {
		_ = port.Close()
		return nil, fmt.Errorf("configure serial read cancellation: %w", err)
	}
	return port, nil
}

func modeFromConfig(cfg Config) (*serial.Mode, error) {
	mode := &serial.Mode{
		BaudRate: cfg.BaudRate,
		DataBits: cfg.DataBits,
	}

	switch strings.ToLower(cfg.Parity) {
	case "", "none":
		mode.Parity = serial.NoParity
	case "odd":
		mode.Parity = serial.OddParity
	case "even":
		mode.Parity = serial.EvenParity
	case "mark":
		mode.Parity = serial.MarkParity
	case "space":
		mode.Parity = serial.SpaceParity
	default:
		return nil, fmt.Errorf("unsupported parity %q", cfg.Parity)
	}

	switch strings.ToLower(cfg.StopBits) {
	case "", "1":
		mode.StopBits = serial.OneStopBit
	case "1.5":
		mode.StopBits = serial.OnePointFiveStopBits
	case "2":
		mode.StopBits = serial.TwoStopBits
	default:
		return nil, fmt.Errorf("unsupported stop bits %q", cfg.StopBits)
	}

	return mode, nil
}
