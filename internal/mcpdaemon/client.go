package mcpdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/mcpserver"
)

type Request struct {
	ClientID  string
	RequestID string
	ID        uint64
	Operation string
	Input     json.RawMessage
}
type Reply struct {
	Output json.RawMessage
	Fault  *debugsession.Fault
}
type Client struct {
	Started  bool
	rpc      *rpc.Client
	sequence atomic.Uint64
	Meta     metadata
}

func Connect(ctx context.Context, opts Options) (*Client, error) {
	dir, err := StateDirectory(opts.StateDir, opts.Demo)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "endpoint.json"))
	if err != nil {
		return nil, err
	}
	var meta metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta.Demo != opts.Demo {
		return nil, ErrIncompatible
	}
	return connectEndpoint(ctx, meta)
}

func connectEndpoint(ctx context.Context, meta metadata) (*Client, error) {
	host, _, err := net.SplitHostPort(meta.Address)
	if err != nil || host != "127.0.0.1" || len(meta.Token) != 64 {
		return nil, errors.New("invalid xserial IPC endpoint")
	}
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", meta.Address)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if _, err := io.WriteString(conn, meta.Token); err != nil {
		_ = conn.Close()
		return nil, err
	}
	var authenticated [1]byte
	if _, err := io.ReadFull(conn, authenticated[:]); err != nil || authenticated[0] != 1 {
		_ = conn.Close()
		return nil, errors.New("authenticate xserial IPC failed")
	}
	_ = conn.SetDeadline(time.Time{})
	if meta.Protocol != protocolVersion {
		_ = conn.Close()
		return nil, ErrIncompatible
	}
	return &Client{rpc: rpc.NewClient(conn), Meta: meta}, nil
}

func (c *Client) Close() error { return c.rpc.Close() }

func (c *Client) Call(ctx context.Context, operation string, input, output any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	identity := mcpserver.RequestIdentity(ctx)
	req := Request{ClientID: identity.ClientID, RequestID: identity.RequestID, ID: c.sequence.Add(1), Operation: operation, Input: data}
	var reply Reply
	call := c.rpc.Go("Device.Call", req, &reply, make(chan *rpc.Call, 1))
	select {
	case <-ctx.Done():
		// Cancellation is a separate RPC, leaving this client's connection and
		// terminal connection intact. The original reply is drained by net/rpc.
		c.rpc.Go("Device.Cancel", req.ID, new(bool), make(chan *rpc.Call, 1))
		return ctx.Err()
	case result := <-call.Done:
		if result.Error != nil {
			return fmt.Errorf("MCP daemon connection ended; reconnect the MCP server: %w", result.Error)
		}
	}
	if output != nil && len(reply.Output) > 0 {
		if err := json.Unmarshal(reply.Output, output); err != nil {
			return err
		}
	}
	if reply.Fault != nil {
		return reply.Fault
	}
	return nil
}
