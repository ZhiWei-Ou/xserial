package mcpdaemon

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
)

type Caller interface {
	Call(context.Context, string, any, any) error
}

// Publication owns IPC only. Closing it cancels readers and removes discovery
// metadata; the terminal remains the sole owner of the serial session.
type Publication struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func Publish(parent context.Context, opts Options, session *debugsession.Session) (*Publication, error) {
	dir, err := StateDirectory(opts.StateDir, opts.Demo)
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := protectDirectory(dir); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	meta := metadata{Protocol: protocolVersion, Address: listener.Addr().String(), Token: randomToken(), PID: os.Getpid(), Demo: opts.Demo, Version: opts.Version}
	path := filepath.Join(dir, rand.Text()+".json")
	data, err := json.Marshal(meta)
	if err == nil {
		err = os.WriteFile(path, data, 0600)
	}
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	p := &Publication{cancel: cancel, done: make(chan struct{})}
	go func() {
		err := serveRPC(ctx, listener, meta.Token, sessionCaller{session}, cancel, false)
		err = errors.Join(err, os.Remove(path))
		p.err = err
		close(p.done)
	}()
	return p, nil
}

func (p *Publication) Close() error { p.cancel(); <-p.done; return p.err }

type sessionCaller struct{ session *debugsession.Session }

func (c sessionCaller) Call(ctx context.Context, op string, input, output any) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	var result any
	switch op {
	case "status":
		result = c.session.Status()
	case "read":
		in, decodeErr := decode[debugsession.ReadInput](raw)
		if decodeErr != nil {
			return decodeErr
		}
		result, err = c.session.Read(ctx, in)
	case "send":
		in, decodeErr := decode[debugsession.SendInput](raw)
		if decodeErr != nil {
			return decodeErr
		}
		result, err = c.session.Send(ctx, in)
	default:
		return &debugsession.Fault{Code: "invalid_argument", Message: "unsupported terminal operation"}
	}
	return errors.Join(err, assign(output, result))
}

func assign(output, result any) error {
	if output == nil {
		return nil
	}
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, output)
}
