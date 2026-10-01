package hexdata

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseUserHexFormats(t *testing.T) {
	want := []byte{0xAA, 1, 0xFF, 0}
	for _, input := range []string{"AA 01 ff 00", "AA01ff00", "AA,01,ff,00", "0xAA 0X01 0xff 0x00", "\nAA\t01 ff\r\n00"} {
		got, err := Parse(input)
		if err != nil || !bytes.Equal(got, want) || Format(got) != "AA 01 FF 00" {
			t.Fatalf("Parse(%q) = % X, %v", input, got, err)
		}
	}
	for _, input := range []string{"A", "AA 1", "0x", "0xAABB", "AA gg", "AA é", "AA:01"} {
		if _, err := Parse(input); err == nil || !strings.Contains(err.Error(), "character") {
			t.Fatalf("Parse(%q) error = %v", input, err)
		}
	}
	if got := ASCII([]byte{'A', 0x1B, '\r', '\n', 0xFF, '~'}); got != "A....~" {
		t.Fatalf("unsafe ASCII = %q", got)
	}
}
