package mcpdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/ZhiWei-Ou/xserial/internal/mcpserver"
)

type attachment struct {
	client *Client
	status debugsession.Status
}
type router struct {
	ctx       context.Context
	dir       string
	audit     *auditLog
	scanMu    sync.Mutex
	mu        sync.Mutex
	terminals map[string]*attachment // discovery path -> authenticated live IPC
}

func newRouter(dir string, audit *auditLog) *router {
	return &router{dir: dir, audit: audit, terminals: make(map[string]*attachment)}
}

func (r *router) run(ctx context.Context) {
	defer r.close()
	r.scan(ctx)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.scan(ctx)
		}
	}
}

func (r *router) scan(ctx context.Context) {
	r.scanMu.Lock()
	defer r.scanMu.Unlock()
	paths, _ := filepath.Glob(filepath.Join(r.dir, "sessions", "*.json"))
	seen := make(map[string]bool)
	for _, path := range paths {
		if ctx.Err() != nil {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var meta metadata
		if json.Unmarshal(data, &meta) != nil {
			continue
		}
		r.mu.Lock()
		current := r.terminals[path]
		r.mu.Unlock()
		if current != nil && current.client.Meta.Token != meta.Token {
			r.remove(path)
			current = nil
		}
		probeCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		wasNew := current == nil
		if current == nil {
			client, connectErr := connectEndpoint(probeCtx, meta)
			if connectErr != nil {
				cancel()
				if errors.Is(connectErr, ErrIncompatible) {
					r.audit.event("mcp.session_incompatible", "endpoint", filepath.Base(path), "result", "restart_terminal")
				}
				continue
			}
			current = &attachment{client: client}
		}
		var status debugsession.Status
		err = current.client.Call(probeCtx, "status", struct{}{}, &status)
		cancel()
		if ctx.Err() != nil {
			if wasNew {
				_ = current.client.Close()
			}
			return
		}
		if err != nil {
			_ = current.client.Close()
			r.remove(path)
			continue
		}
		seen[path] = true
		r.mu.Lock()
		old := current.status
		current.status = status
		r.terminals[path] = current
		r.mu.Unlock()
		if old.SessionID != status.SessionID {
			r.transition("mcp.detach", old)
			r.transition("mcp.attach", status)
		}
	}
	r.mu.Lock()
	var missing []string
	for path := range r.terminals {
		if !seen[path] {
			missing = append(missing, path)
		}
	}
	r.mu.Unlock()
	for _, path := range missing {
		r.remove(path)
	}
}

func (r *router) transition(event string, status debugsession.Status) {
	if status.SessionID == "" {
		return
	}
	port := ""
	if status.Connection != nil {
		port = status.Connection.Port
	}
	r.audit.event(event, "session_id", status.SessionID, "port", port, "result", "completed")
}

func (r *router) remove(path string) {
	r.mu.Lock()
	current := r.terminals[path]
	delete(r.terminals, path)
	r.mu.Unlock()
	if current != nil {
		_ = current.client.Close()
		r.transition("mcp.detach", current.status)
	}
}
func (r *router) close() {
	r.scanMu.Lock()
	defer r.scanMu.Unlock()
	r.mu.Lock()
	var paths []string
	for path := range r.terminals {
		paths = append(paths, path)
	}
	r.mu.Unlock()
	for _, path := range paths {
		r.remove(path)
	}
}

func (r *router) Call(ctx context.Context, op string, input, output any) (err error) {
	if r.ctx != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		stop := context.AfterFunc(r.ctx, cancel)
		defer stop()
		defer cancel()
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	if op == "audit" {
		in, err := decode[AuditInput](raw)
		if err != nil {
			return err
		}
		result, err := r.audit.read(ctx, in)
		return errors.Join(err, assign(output, result))
	}
	identity := mcpserver.RequestIdentity(ctx)
	if op == "tool_rejected" {
		in, err := decode[mcpserver.RejectedTool](raw)
		if err != nil {
			return err
		}
		event := "mcp.tool_rejected"
		switch in.Operation {
		case "status", "read", "send":
			event = "mcp." + in.Operation
		}
		r.audit.event(event, "client_id", identity.ClientID, "request_id", identity.RequestID, "session_id", in.SessionID, "duration_ms", in.DurationMS, "result", "failed", "error", "invalid tool arguments or unknown tool")
		return nil
	}
	fields := []any{"client_id", identity.ClientID, "request_id", identity.RequestID}
	started := time.Now()
	defer func() {
		result := "completed"
		if err != nil {
			result = "failed"
			if ctx.Err() != nil {
				result = "canceled"
			}
			fields = append(fields, "error", err.Error())
		}
		fields = append(fields, "duration_ms", time.Since(started).Milliseconds(), "result", result)
		r.audit.event("mcp."+op, fields...)
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	switch op {
	case "client_connected":
		return nil
	case "status":
		r.scan(ctx)
		result := debugsession.StatusResult{Sessions: []debugsession.Status{}}
		r.mu.Lock()
		for _, terminal := range r.terminals {
			if terminal.status.SessionID != "" {
				result.Sessions = append(result.Sessions, terminal.status)
			}
		}
		r.mu.Unlock()
		sort.Slice(result.Sessions, func(i, j int) bool { return result.Sessions[i].SessionID < result.Sessions[j].SessionID })
		fields = append(fields, "sessions", len(result.Sessions))
		return assign(output, result)
	case "read", "send":
		var sessionID string
		if op == "send" {
			in, decodeErr := decode[debugsession.SendInput](raw)
			if decodeErr != nil {
				return decodeErr
			}
			sessionID = in.SessionID
			data, _, decodeErr := debugsession.DecodeSend(in)
			if decodeErr != nil {
				return decodeErr
			}
			fields = append(fields, "bytes", len(data), "preview", data[:min(len(data), 256)], "truncated", len(data) > 256)
		} else {
			in, decodeErr := decode[debugsession.ReadInput](raw)
			if decodeErr != nil {
				return decodeErr
			}
			sessionID = in.SessionID
			fields = append(fields, "cursor", in.Cursor)
		}
		fields = append(fields, "session_id", sessionID)
		r.mu.Lock()
		var client *Client
		port, cursor := "", ""
		attachedCount := 0
		for _, terminal := range r.terminals {
			if terminal.status.SessionID != "" {
				attachedCount++
			}
			if terminal.status.SessionID == sessionID && sessionID != "" {
				cursor = terminal.status.Cursor
				client = terminal.client
				if terminal.status.Connection != nil {
					port = terminal.status.Connection.Port
				}
			}
		}
		empty := attachedCount == 0
		r.mu.Unlock()
		fields = append(fields, "port", port)
		if client == nil {
			if empty {
				return &debugsession.Fault{Code: "no_session", Message: "no attached terminal sessions; connect with xserial <port> [cfg]"}
			}
			return &debugsession.Fault{Code: "detached", Message: "serial session is detached; refresh serial_status"}
		}
		if op == "read" {
			var result debugsession.ReadResult
			err = client.Call(ctx, op, input, &result)
			fields = append(fields, "bytes", result.Bytes, "next_cursor", result.NextCursor, "reason", result.Reason)
			var fault *debugsession.Fault
			if err != nil && ctx.Err() == nil && !errors.As(err, &fault) {
				err = &debugsession.Fault{Code: "detached", Message: "terminal IPC disconnected"}
			}
			return errors.Join(err, assign(output, result))
		}
		result := debugsession.SendResult{SessionID: sessionID, Cursor: cursor, Delivery: "unknown"}
		err = client.Call(ctx, op, input, &result)
		fields = append(fields, "cursor", result.Cursor, "delivery", result.Delivery)
		return errors.Join(err, assign(output, result))
	default:
		return &debugsession.Fault{Code: "invalid_argument", Message: "unsupported MCP operation"}
	}
}
