package hexdata

import (
	"bytes"
	"testing"
)

func TestFramingReassemblesSplitAndJoinedResponses(t *testing.T) {
	for _, tc := range []struct {
		rule  string
		frame []byte
	}{
		{"fixed:9", []byte{1, 3, 4, 0, 100, 0, 101, 0xBA, 0x7A}},
		{"delimiter:0D0A", []byte{1, 2, 3, 13, 10}},
		{"length:2:1:5:be", []byte{1, 3, 4, 0, 100, 0, 101, 0xBA, 0x7A}},
		{"length:0:2:2:le", []byte{3, 0, 1, 2, 3}},
		{"modbus-read", []byte{1, 3, 4, 0, 100, 0, 101, 0xBA, 0x7A}},
	} {
		cfg, err := ParseFrameConfig(tc.rule)
		if err != nil {
			t.Fatal(err)
		}
		framer := NewFramer(cfg)
		frames, err := framer.Push(tc.frame[:2])
		if err != nil || len(frames) != 0 {
			t.Fatalf("partial %s = %v, %v", tc.rule, frames, err)
		}
		joined := append(append([]byte(nil), tc.frame[2:]...), tc.frame...)
		frames, err = framer.Push(joined)
		if err != nil || len(frames) != 2 || !bytes.Equal(frames[0], tc.frame) || !bytes.Equal(frames[1], tc.frame) || framer.Pending() != 0 {
			t.Fatalf("%s frames = % X, %v", tc.rule, frames, err)
		}
	}
}

func TestFramerRejectsInvalidLengthsAndClearsPartialData(t *testing.T) {
	for _, spec := range []string{"fixed:0", "delimiter:", "length:0:3:4:be", "length:2:2:1:le", "length:0:1:2:other", "fixed:999999"} {
		if _, err := ParseFrameConfig(spec); err == nil {
			t.Fatalf("accepted %q", spec)
		}
	}
	cfg, _ := ParseFrameConfig("length:0:4:4:be")
	f := NewFramer(cfg)
	if _, err := f.Push([]byte{0xFF, 0xFF, 0xFF, 0xFF}); err == nil || f.Pending() != 0 {
		t.Fatal("unbounded length accepted")
	}
	cfg, _ = ParseFrameConfig("delimiter:0D0A")
	f = NewFramer(cfg)
	if _, err := f.Push(bytes.Repeat([]byte{0xAA}, maxFrameBytes+1)); err == nil || f.Pending() != 0 {
		t.Fatal("unbounded incomplete frame")
	}
	f.Push([]byte{0xAA})
	if f.Reset() != 1 || f.Pending() != 0 {
		t.Fatal("disconnect did not reset partial frame")
	}
}
