package session

import "io"

const DefaultPrefixKey byte = 0x01

type PrefixAction int

const (
	ActionNone PrefixAction = iota
	ActionHelp
	ActionUpload
	ActionQuit
	ActionWroteLiteralPrefix
)

type PrefixMachine struct {
	prefixKey   byte
	afterPrefix bool
}

func NewPrefixMachine(prefixKey byte) *PrefixMachine {
	return &PrefixMachine{prefixKey: prefixKey}
}

func (m *PrefixMachine) HandleByte(b byte, serial io.Writer) (PrefixAction, error) {
	if !m.afterPrefix {
		if b == m.prefixKey {
			m.afterPrefix = true
			return ActionNone, nil
		}
		_, err := serial.Write([]byte{b})
		return ActionNone, err
	}

	m.afterPrefix = false
	switch b {
	case m.prefixKey:
		_, err := serial.Write([]byte{m.prefixKey})
		return ActionWroteLiteralPrefix, err
	case 'h', 'H', '?':
		return ActionHelp, nil
	case 'u', 'U':
		return ActionUpload, nil
	case 'q', 'Q':
		return ActionQuit, nil
	default:
		_, err := serial.Write([]byte{m.prefixKey, b})
		return ActionNone, err
	}
}
