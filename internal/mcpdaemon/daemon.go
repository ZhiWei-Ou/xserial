package mcpdaemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/rpc"
	"strconv"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/mcpserver"
)

// serveRPC authenticates local peers before exposing operations. It cancels
// handlers on EOF and waits for them on shutdown; closing it never closes serial I/O.
func serveRPC(ctx context.Context, listener net.Listener, token string, caller Caller, stop context.CancelFunc, allowStop bool) error {
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
			var credential [64]byte
			if _, err := io.ReadFull(conn, credential[:]); err != nil || subtle.ConstantTimeCompare(credential[:], []byte(token)) != 1 {
				return
			}
			if _, err := conn.Write([]byte{1}); err != nil {
				return
			}
			_ = conn.SetDeadline(time.Time{})
			clientCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			p := &peer{clientID: rand.Text(), ctx: clientCtx, service: caller, active: make(map[uint64]context.CancelFunc), canceled: make(map[uint64]bool), finished: make(map[uint64]bool)}
			if allowStop {
				p.stop = stop
			}
			server := rpc.NewServer()
			if err := server.RegisterName("Device", p); err != nil {
				return
			}
			server.ServeConn(&watchedConn{Conn: conn, cancel: cancel})
		}()
	}
	// The owner cancels before closing listener, including on an accept failure.
	stop()
	<-watcherDone
	workers.Wait()
	return acceptErr
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
	clientID        string
	ctx             context.Context
	service         Caller
	stop            context.CancelFunc
	mu              sync.Mutex
	active          map[uint64]context.CancelFunc
	canceled        map[uint64]bool
	finished        map[uint64]bool
	finishedThrough uint64
}

func (p *peer) Call(req Request, reply *Reply) error {
	clientID, requestID := req.ClientID, req.RequestID
	if clientID == "" {
		clientID = p.clientID
	}
	if requestID == "" {
		requestID = strconv.FormatUint(req.ID, 10)
	}
	ctx, cancel := context.WithCancel(mcpserver.WithIdentity(p.ctx, clientID, requestID))
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
	var err error
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if req.Operation == "stop" && p.stop != nil {
		p.stop()
	} else {
		err = p.service.Call(ctx, req.Operation, req.Input, &reply.Output)
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
