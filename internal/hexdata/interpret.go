package hexdata

import (
	"encoding/binary"
	"fmt"
	"math"
)

type Interpretation struct{ Type, LittleEndian, BigEndian string }

// Interpret uses exactly the selected width, never pads a partial field.
func Interpret(data []byte) []Interpretation {
	var values []Interpretation
	switch len(data) {
	case 1:
		values = append(values, Interpretation{"uint8", fmt.Sprint(data[0]), fmt.Sprint(data[0])}, Interpretation{"int8", fmt.Sprint(int8(data[0])), fmt.Sprint(int8(data[0]))})
	case 2:
		le, be := binary.LittleEndian.Uint16(data), binary.BigEndian.Uint16(data)
		values = append(values, Interpretation{"uint16", fmt.Sprint(le), fmt.Sprint(be)}, Interpretation{"int16", fmt.Sprint(int16(le)), fmt.Sprint(int16(be))})
	case 4:
		le, be := binary.LittleEndian.Uint32(data), binary.BigEndian.Uint32(data)
		values = append(values, Interpretation{"uint32", fmt.Sprint(le), fmt.Sprint(be)}, Interpretation{"int32", fmt.Sprint(int32(le)), fmt.Sprint(int32(be))},
			Interpretation{"float32", fmt.Sprint(math.Float32frombits(le)), fmt.Sprint(math.Float32frombits(be))})
	case 8:
		le, be := binary.LittleEndian.Uint64(data), binary.BigEndian.Uint64(data)
		values = append(values, Interpretation{"uint64", fmt.Sprint(le), fmt.Sprint(be)}, Interpretation{"int64", fmt.Sprint(int64(le)), fmt.Sprint(int64(be))},
			Interpretation{"float64", fmt.Sprint(math.Float64frombits(le)), fmt.Sprint(math.Float64frombits(be))})
	}
	return values
}
