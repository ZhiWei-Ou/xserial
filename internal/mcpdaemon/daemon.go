package mcpdaemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
)

func Run(ctx context.Context, opts Options, deps debugsession.Dependencies) error {
	dir, err := StateDirectory(opts.StateDir, opts.Demo)
	if err != nil {
		return err
	}
	lock, err := acquire(filepath.Join(dir, "daemon.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	meta := metadata{protocolVersion, listener.Addr().String(), randomToken(), os.Getpid(), opts.Demo, opts.Version}
	if err := writeMetadata(dir, meta); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(dir, "endpoint.json"))
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	service := debugsession.New(ctx, deps)
	connections := make(map[net.Conn]struct{})
	var mu sync.Mutex
	var workers sync.WaitGroup
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		<-ctx.Done()
		_ = listener.Close()
		mu.Lock()
		for conn := range connections {
			_ = conn.Close()
		}
		mu.Unlock()
	}()
	var acceptErr error
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() == nil {
				acceptErr = err
			}
			break
		}
		mu.Lock()
		if ctx.Err() != nil {
			mu.Unlock()
			_ = conn.Close()
			break
		}
		connections[conn] = struct{}{}
		mu.Unlock()
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer conn.Close()
			defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
			_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
			var token [64]byte
			if _, err := io.ReadFull(conn, token[:]); err != nil || subtle.ConstantTimeCompare(token[:], []byte(meta.Token)) != 1 {
				return
			}
			if _, err := conn.Write([]byte{1}); err != nil {
				return
			}
			_ = conn.SetDeadline(time.Time{})
			owner := rand.Text()
			clientCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			peer := &peer{ctx: clientCtx, service: service, owner: owner, stop: stop,
				active: make(map[uint64]context.CancelFunc), canceled: make(map[uint64]bool), finished: make(map[uint64]bool)}
			server := rpc.NewServer()
			if err := server.RegisterName("Device", peer); err != nil {
				return
			}
			server.ServeConn(&watchedConn{Conn: conn, cancel: cancel})
			service.Release(owner)
		}()
	}
	stop()
	<-watcherDone
	workers.Wait()
	return errors.Join(acceptErr, service.Shutdown())
}

// net/rpc waits for handlers before ServeConn returns. Cancel when its reader
// observes EOF so a disconnected client's long read exits before that wait.
type watchedConn struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *watchedConn) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	if err != nil {
		c.cancel()
	}
	return n, err
}

type peer struct {
	ctx             context.Context
	service         *debugsession.Service
	owner           string
	stop            context.CancelFunc
	mu              sync.Mutex
	active          map[uint64]context.CancelFunc
	canceled        map[uint64]bool
	finished        map[uint64]bool
	finishedThrough uint64
}

func (p *peer) Call(req Request, reply *Reply) error {
	ctx, cancel := context.WithCancel(p.ctx)
	p.mu.Lock()
	p.active[req.ID] = cancel
	if p.canceled[req.ID] {
		cancel()
	}
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		delete(p.active, req.ID)
		delete(p.canceled, req.ID)
		p.finished[req.ID] = true
		for p.finished[p.finishedThrough+1] {
			p.finishedThrough++
			delete(p.finished, p.finishedThrough)
		}
		p.mu.Unlock()
	}()
	output, err := p.dispatch(ctx, req)
	if output != nil {
		var encodeErr error
		reply.Output, encodeErr = json.Marshal(output)
		err = errors.Join(err, encodeErr)
	}
	if err != nil {
		var fault *debugsession.Fault
		if errors.As(err, &fault) {
			reply.Fault = fault
		} else {
			code := "device_error"
			if errors.Is(err, context.Canceled) {
				code = "canceled"
			}
			reply.Fault = &debugsession.Fault{Code: code, Message: err.Error()}
		}
	}
	return nil
}

func (p *peer) Cancel(id uint64, result *bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id <= p.finishedThrough || p.finished[id] {
		return nil
	}
	if cancel := p.active[id]; cancel != nil {
		cancel()
	} else {
		p.canceled[id] = true
	}
	*result = true
	return nil
}

func decode[T any](raw json.RawMessage) (T, error) {
	var value T
	err := json.Unmarshal(raw, &value)
	if err != nil {
		return value, &debugsession.Fault{Code: "invalid_argument", Message: err.Error()}
	}
	return value, nil
}

func (p *peer) dispatch(ctx context.Context, req Request) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch req.Operation {
	case "list":
		return p.service.List()
	case "status":
		return p.service.Status(p.owner), nil
	case "open":
		in, err := decode[debugsession.Connection](req.Input)
		if err != nil {
			return nil, err
		}
		return p.service.Open(ctx, p.owner, in)
	case "send":
		in, err := decode[debugsession.SendInput](req.Input)
		if err != nil {
			return nil, err
		}
		return p.service.Send(ctx, p.owner, in)
	case "read":
		in, err := decode[debugsession.ReadInput](req.Input)
		if err != nil {
			return nil, err
		}
		return p.service.Read(ctx, in)
	case "close":
		in, err := decode[debugsession.SessionInput](req.Input)
		if err != nil {
			return nil, err
		}
		return p.service.Close(ctx, p.owner, in)
	case "stop":
		p.stop()
		return struct{}{}, nil
	default:
		return nil, fmt.Errorf("unknown daemon operation %q", req.Operation)
	}
}
