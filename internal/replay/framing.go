package replay

import (
	"fmt"
	"strings"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
)

type FrameConfig struct {
	Bytes hexdata.FrameConfig
	Gap   time.Duration
}

func ParseFrameConfig(rule string) (FrameConfig, error) {
	if rule == "" || rule == "newline" {
		return FrameConfig{Bytes: hexdata.FrameConfig{Kind: "delimiter", Delimiter: []byte{'\n'}}}, nil
	}
	if strings.HasPrefix(rule, "gap:") {
		gap, err := time.ParseDuration(strings.TrimPrefix(rule, "gap:"))
		if err != nil || gap <= 0 {
			return FrameConfig{}, fmt.Errorf("invalid frame rule %q; gap must be a positive duration, e.g. gap:50ms", rule)
		}
		return FrameConfig{Gap: gap}, nil
	}
	bytes, err := hexdata.ParseFrameConfig(rule)
	return FrameConfig{Bytes: bytes}, err
}
