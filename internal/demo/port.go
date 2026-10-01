// Package demo provides a simulated device through the normal serial port contract.
package demo

import (
	"io"
	"sync"

	"github.com/ZhiWei-Ou/xserial/internal/hexdata"
)

// Port models reads that block, can split a response, and unblock on Close.
// The read queue is bounded and applies backpressure to the single writer.
type Port struct {
	responses chan []byte // Write sends; Close signals via closed instead of closing this channel.
	closed    chan struct{}
	once      sync.Once
	pending   []byte // Owned exclusively by the backend reader.
}

func NewPort() *Port { return &Port{responses: make(chan []byte, 8), closed: make(chan struct{})} }

func (p *Port) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for len(p.pending) == 0 {
		select {
		case <-p.closed:
			return 0, io.EOF
		case data := <-p.responses:
			p.pending = data
		}
	}
	n := copy(dst, p.pending)
	p.pending = p.pending[n:]
	return n, nil
}

func (p *Port) Write(data []byte) (int, error) {
	select {
	case <-p.closed:
		return 0, io.ErrClosedPipe
	default:
	}
	response := responseFor(data)
	chunks := [][]byte{response}
	if len(data) > 0 && data[0] == 3 {
		chunks = [][]byte{response[:3], response[3:]}
	} else if len(data) > 0 && data[0] == 4 {
		chunks = [][]byte{append(append([]byte(nil), response...), response...)}
	}
	for _, chunk := range chunks {
		select {
		case p.responses <- chunk:
		case <-p.closed:
			return 0, io.ErrClosedPipe
		}
	}
	return len(data), nil
}

func responseFor(request []byte) []byte {
	if len(request) == 0 {
		return nil
	}
	address := request[0]
	response := []byte{address, 0x83, 0x03}
	if len(request) == 8 && request[1] == 3 {
		ok, _ := hexdata.VerifyChecksum(request, "crc16-modbus")
		quantity := int(request[4])<<8 | int(request[5])
		if ok && quantity >= 1 && quantity <= 16 {
			response = []byte{address, 3, byte(quantity * 2)}
			for i := range quantity {
				response = append(response, byte((100+i)>>8), byte(100+i))
			}
		}
	}
	response, _ = hexdata.AppendChecksum(response, "crc16-modbus")
	if address == 2 {
		response[len(response)-1] ^= 0xFF
	}
	return response
}

func (p *Port) Close() error { p.once.Do(func() { close(p.closed) }); return nil }
