package hexdata

import (
	"bytes"
	"testing"
)

func TestModbusKnownRequestAndCheckValue(t *testing.T) {
	if crc := CRC16Modbus([]byte("123456789")); crc != 0x4B37 {
		t.Fatalf("check = %04X", crc)
	}
	request := []byte{1, 3, 0, 0, 0, 2}
	frame, err := AppendChecksum(request, "crc16-modbus")
	if err != nil || !bytes.Equal(frame, []byte{1, 3, 0, 0, 0, 2, 0xC4, 0x0B}) {
		t.Fatalf("frame = % X, %v", frame, err)
	}
	valid, err := VerifyChecksum(frame, "crc16-modbus")
	if err != nil || !valid {
		t.Fatalf("valid=%v, err=%v", valid, err)
	}
	frame[3] ^= 1
	if valid, _ := VerifyChecksum(frame, "crc16-modbus"); valid {
		t.Fatal("corruption was accepted")
	}
	if !bytes.Equal(request, []byte{1, 3, 0, 0, 0, 2}) {
		t.Fatal("append modified original input")
	}
}

func TestExplicitChecksumAlgorithms(t *testing.T) {
	for _, tc := range []struct {
		algorithm string
		want      byte
	}{{"sum8", 0x03}, {"xor8", 0xFF}} {
		got, err := Checksum([]byte{0xFF, 2, 2}, tc.algorithm)
		if err != nil || len(got) != 1 || got[0] != tc.want {
			t.Fatalf("%s = % X, %v", tc.algorithm, got, err)
		}
	}
	if _, err := Checksum(nil, "crc"); err == nil {
		t.Fatal("ambiguous algorithm accepted")
	}
}
