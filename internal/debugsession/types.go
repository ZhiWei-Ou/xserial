// Package debugsession provides persistent, cursor-based device debugging.
package debugsession

import (
	"fmt"
)

const HistoryBytes = 1 << 20

type Fault struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (f *Fault) Error() string         { return f.Message }
func fault(code, message string) error { return &Fault{code, message} }

type Connection struct {
	Port     string `json:"port"`
	Baud     int    `json:"baud,omitempty" jsonschema:"Baud rate; default 115200"`
	DataBits int    `json:"data_bits,omitempty" jsonschema:"Data bits, 5 through 8; default 8"`
	Parity   string `json:"parity,omitempty" jsonschema:"N, O, E, M or S; default N"`
	StopBits string `json:"stop_bits,omitempty" jsonschema:"1, 1.5 or 2; default 1"`
}

type StatusResult struct {
	Sessions []Status `json:"sessions"`
}
type Status struct {
	SessionID  string      `json:"session_id,omitempty"`
	State      string      `json:"state"`
	Connection *Connection `json:"connection,omitempty"`
	RXBytes    uint64      `json:"rx_bytes"`
	TXBytes    uint64      `json:"tx_bytes"`
	Cursor     string      `json:"cursor,omitempty"`
	Error      string      `json:"error,omitempty"`
}
type SendInput struct {
	SessionID string `json:"session_id"`
	Data      string `json:"data" jsonschema:"Exact data to send; include newline explicitly for shell commands"`
	Encoding  string `json:"encoding,omitempty" jsonschema:"text, hex or base64; default text"`
	TimeoutMS *int   `json:"timeout_ms,omitempty" jsonschema:"Host write wait budget, 1 through 30000 milliseconds; default 5000. Timeout does not undo bytes already sent."`
}
type SendResult struct {
	SessionID string `json:"session_id"`
	Cursor    string `json:"cursor"`
	Bytes     int    `json:"bytes"`
	Delivery  string `json:"delivery"`
}
type ReadInput struct {
	SessionID string `json:"session_id"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"Previous next_cursor or send cursor; omit for latest output, now for a checkpoint"`
	WaitMS    *int   `json:"wait_ms,omitempty" jsonschema:"Total wait budget in milliseconds, 0 through 30000; default 2000"`
	IdleMS    *int   `json:"idle_ms,omitempty" jsonschema:"Return after this much silence following data, 0 through 5000; default 150"`
	MaxBytes  *int   `json:"max_bytes,omitempty" jsonschema:"Maximum raw bytes returned, 1 through 65536; default 8192"`
}
type ReadResult struct {
	SessionID    string `json:"session_id"`
	Output       string `json:"output"`
	DataBase64   string `json:"data_base64"`
	Bytes        int    `json:"bytes"`
	NextCursor   string `json:"next_cursor"`
	Reason       string `json:"reason"`
	HasMore      bool   `json:"has_more"`
	DroppedBytes uint64 `json:"dropped_bytes"`
	State        string `json:"state"`
	Error        string `json:"error,omitempty"`
}

func invalid(format string, args ...any) error {
	return fault("invalid_argument", fmt.Sprintf(format, args...))
}
