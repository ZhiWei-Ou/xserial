package hexdata

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// CRC16Modbus uses init=0xffff, reflected poly=0xa001, xorout=0.
// On the wire, the low byte precedes the high byte.
func CRC16Modbus(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for range 8 {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// Checksum returns the wire bytes for the explicitly named algorithm.
func Checksum(data []byte, algorithm string) ([]byte, error) {
	switch algorithm {
	case "none":
		return nil, nil
	case "crc16-modbus":
		return binary.LittleEndian.AppendUint16(nil, CRC16Modbus(data)), nil
	case "sum8":
		var sum byte
		for _, b := range data {
			sum += b
		}
		return []byte{sum}, nil
	case "xor8":
		var sum byte
		for _, b := range data {
			sum ^= b
		}
		return []byte{sum}, nil
	default:
		return nil, fmt.Errorf("unknown checksum %q; use none, crc16-modbus, sum8, or xor8", algorithm)
	}
}

func AppendChecksum(data []byte, algorithm string) ([]byte, error) {
	checksum, err := Checksum(data, algorithm)
	if err != nil {
		return nil, err
	}
	return append(append([]byte(nil), data...), checksum...), nil
}

func VerifyChecksum(data []byte, algorithm string) (bool, error) {
	checksum, err := Checksum(nil, algorithm)
	if err != nil {
		return false, err
	}
	if len(checksum) == 0 {
		return false, fmt.Errorf("select a checksum algorithm to verify")
	}
	if len(data) <= len(checksum) {
		return false, fmt.Errorf("not enough bytes for %s", algorithm)
	}
	checksum, _ = Checksum(data[:len(data)-len(checksum)], algorithm)
	return bytes.Equal(data[len(data)-len(checksum):], checksum), nil
}
