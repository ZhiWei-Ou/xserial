package hexdata

import "testing"

func TestSelectedFieldsUseExactWidthAndByteOrder(t *testing.T) {
	for _, tc := range []struct {
		data         []byte
		kind, le, be string
	}{
		{[]byte{0xFF}, "int8", "-1", "-1"},
		{[]byte{0, 100}, "uint16", "25600", "100"},
		{[]byte{0xFF, 0xFF}, "int16", "-1", "-1"},
		{[]byte{0, 0, 0x80, 0x3F}, "float32", "1", "4.6006e-41"},
		{[]byte{0, 0, 0, 0, 0, 0, 0xF0, 0x3F}, "float64", "1", "3.03865e-319"},
	} {
		found := false
		for _, value := range Interpret(tc.data) {
			if value.Type == tc.kind {
				found = true
				if value.LittleEndian != tc.le || value.BigEndian != tc.be {
					t.Fatalf("%s = %+v", tc.kind, value)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s", tc.kind)
		}
	}
	if got := Interpret([]byte{1, 2, 3}); len(got) != 0 {
		t.Fatal("partial field silently padded")
	}
}
