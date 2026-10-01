package demo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
	"github.com/ZhiWei-Ou/xserial/internal/middleware"
)

type frontendFunc func(context.Context, middleware.Endpoint) error

func (f frontendFunc) Run(ctx context.Context, e middleware.Endpoint) error { return f(ctx, e) }

func TestDemoUsesSessionByteFlowAndCloseProtocol(t *testing.T) {
	port := NewPort()
	frontend := frontendFunc(func(ctx context.Context, e middleware.Endpoint) error {
		events := e.Events()
		for _, address := range []byte{1, 2, 3, 4} {
			request, _ := hexdata.AppendChecksum([]byte{address, 3, 0, 0, 0, 2}, "crc16-modbus")
			if err := e.Send(ctx, request); err != nil {
				return err
			}
			var response []byte
			chunks := 0
			responseSize := 9
			if address == 4 {
				responseSize = 18
			}
			for len(response) < responseSize {
				select {
				case event := <-events:
					if received, ok := event.(middleware.Received); ok {
						response = append(response, received.Data...)
						chunks++
					}
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			ok, err := hexdata.VerifyChecksum(response[:9], "crc16-modbus")
			if err != nil || ok != (address != 2) {
				t.Fatalf("address %d response = % X, valid=%v, err=%v", address, response, ok, err)
			}
			if address == 3 && chunks != 2 {
				t.Fatalf("split response chunks = %d", chunks)
			}
			if address == 4 {
				cfg, _ := hexdata.ParseFrameConfig("modbus-read")
				frames, err := hexdata.NewFramer(cfg).Push(response)
				if err != nil || chunks != 1 || len(frames) != 2 || !bytes.Equal(frames[0], frames[1]) {
					t.Fatalf("joined response: chunks=%d frames=% X err=%v", chunks, frames, err)
				}
			}
		}
		e.Quit()
		return nil
	})
	if err := middleware.New(middleware.Config{Port: port, Frontend: frontend}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := port.Write([]byte{1}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("session did not close demo: %v", err)
	}
}

func TestDemoReadPreservesBytesAcrossSmallBuffersAndCloseIsIdempotent(t *testing.T) {
	port := NewPort()
	request, _ := hexdata.AppendChecksum([]byte{1, 3, 0, 0, 0, 2}, "crc16-modbus")
	if _, err := port.Write(request); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 9)
	if _, err := io.ReadFull(port, response[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(port, response[1:]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response[:7], []byte{1, 3, 4, 0, 100, 0, 101}) {
		t.Fatalf("response = % X", response)
	}
	port.Close()
	port.Close()
	if _, err := port.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("closed read = %v", err)
	}
}
