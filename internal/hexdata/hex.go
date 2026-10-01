// Package hexdata provides byte editing and interpretation for serial debugging.
package hexdata

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// Parse accepts compact hex, whitespace/comma-separated hex, and 0x-prefixed bytes.
// Error positions are one-based character offsets in the original input.
func Parse(input string) ([]byte, error) {
	runes := []rune(input)
	var data []byte
	for i := 0; i < len(runes); {
		if unicode.IsSpace(runes[i]) || runes[i] == ',' {
			i++
			continue
		}
		start := i
		for i < len(runes) && !unicode.IsSpace(runes[i]) && runes[i] != ',' {
			i++
		}
		token := string(runes[start:i])
		prefixed := strings.HasPrefix(token, "0x") || strings.HasPrefix(token, "0X")
		if prefixed {
			token = token[2:]
			start += 2
			if len(token) != 2 {
				return nil, fmt.Errorf("character %d: 0x requires exactly two hex digits", start+1)
			}
		}
		for offset, r := range token {
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return nil, fmt.Errorf("character %d: %q is not a hex digit", start+offset+1, r)
			}
		}
		if len(token)%2 != 0 {
			return nil, fmt.Errorf("character %d: incomplete byte; use two hex digits", start+len(token))
		}
		decoded, _ := hex.DecodeString(token) // Every digit and the length were checked above.
		data = append(data, decoded...)
	}
	return data, nil
}

func Format(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var out strings.Builder
	for i, b := range data {
		if i > 0 {
			out.WriteByte(' ')
		}
		fmt.Fprintf(&out, "%02X", b)
	}
	return out.String()
}

// ASCII never executes device control characters in the workbench.
func ASCII(data []byte) string {
	var out strings.Builder
	for _, b := range data {
		if b >= 32 && b <= 126 {
			out.WriteByte(b)
		} else {
			out.WriteByte('.')
		}
	}
	return out.String()
}
