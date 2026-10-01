package middleware

import (
	"context"
	"errors"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/logging"
)

func (e *endpoint) sendRemote(ctx context.Context, generation uint64, status debugsession.Status, data []byte, checkpoint func()) error {
	started := time.Now()
	port := ""
	if status.Connection != nil {
		port = status.Connection.Port
	}
	err := e.send(ctx, data, generation, checkpoint, "mcp")
	if e.remoteAudit != nil {
		result := "completed"
		if err != nil {
			result = "failed"
			if ctx.Err() != nil {
				result = "canceled"
			}
		}
		preview := data[:min(len(data), 256)]
		line := logging.EventFormatter{}.Format(logging.Entry{Time: time.Now(), Level: logging.InfoLevel, Message: "mcp.send", Fields: []any{"session_id", status.SessionID, "port", port, "bytes", len(data), "duration_ms", time.Since(started).Milliseconds(), "result", result, "preview", preview, "truncated", len(data) > 256}})
		_, _ = e.remoteAudit.Write(line)
	}
	if errors.Is(err, ErrTransferActive) {
		return &debugsession.Fault{Code: "busy", Message: "YMODEM transfer is active"}
	}
	if errors.Is(err, ErrDisconnected) {
		return &debugsession.Fault{Code: "detached", Message: err.Error()}
	}
	return err
}

func debugConnection(cfg ConnectionConfig) debugsession.Connection {
	parity := map[string]string{"none": "N", "odd": "O", "even": "E", "mark": "M", "space": "S"}[cfg.Parity]
	return debugsession.Connection{Port: cfg.PortName, Baud: cfg.BaudRate, DataBits: cfg.DataBits, Parity: parity, StopBits: cfg.StopBits}
}
