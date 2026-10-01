package middleware

import (
	"context"
	"errors"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
)

func (e *endpoint) sendRemote(ctx context.Context, generation uint64, data []byte, checkpoint func()) error {
	err := e.send(ctx, data, generation, checkpoint, "mcp")
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
