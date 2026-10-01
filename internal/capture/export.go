package capture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
)

type Filter struct {
	From  time.Duration
	To    time.Duration // Zero means no upper bound.
	Match []byte
}

func (f Filter) Includes(header Header, record Record) bool {
	elapsed := record.At.Sub(header.Started)
	return elapsed >= f.From && (f.To == 0 || elapsed <= f.To) && (len(f.Match) == 0 || bytes.Contains(record.Data, f.Match))
}

func Export(dst io.Writer, session Session, format string, filter Filter) error {
	if format != "text" && format != "jsonl" {
		return fmt.Errorf("unknown export format %q", format)
	}
	var encoder *json.Encoder
	if format == "jsonl" {
		encoder = json.NewEncoder(dst)
		if err := encoder.Encode(session.Header); err != nil {
			return err
		}
	}
	for _, record := range session.Records {
		if !filter.Includes(session.Header, record) {
			continue
		}
		if format == "jsonl" {
			if err := encoder.Encode(record); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(dst, "%s #%d %-13s %4d  %s", record.At.Format(time.RFC3339Nano), record.Seq, record.Kind, len(record.Data), hexdata.Format(record.Data)); err != nil {
			return err
		}
		if record.Error != "" {
			if _, err := fmt.Fprintf(dst, "  error=%q", record.Error); err != nil {
				return err
			}
		}
		if record.Note != "" {
			if _, err := fmt.Fprintf(dst, "  note=%q", record.Note); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(dst); err != nil {
			return err
		}
	}
	return nil
}
