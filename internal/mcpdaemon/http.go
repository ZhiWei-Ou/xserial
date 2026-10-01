package mcpdaemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const DefaultURL = "http://127.0.0.1:8765/mcp"

func ParseURL(value string) (*url.URL, error) {
	if value == "" {
		value = DefaultURL
	}
	u, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("MCP URL must be a local http URL without credentials, query or fragment")
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil, errors.New("MCP URL host must be localhost, 127.0.0.1 or [::1]")
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return nil, errors.New("invalid MCP URL port")
	}
	if u.Path == "" {
		u.Path = "/mcp"
	}
	return u, nil
}

// Run owns discovery and network listeners. It never opens or closes a port.
func Run(parent context.Context, opts Options) error {
	u, err := ParseURL(opts.URL)
	if err != nil {
		return err
	}
	dir, err := StateDirectory(opts.StateDir, opts.Demo)
	if err != nil {
		return err
	}
	lock, err := acquire(filepath.Join(dir, "daemon.lock"))
	if err != nil {
		if errors.Is(err, ErrRunning) {
			probe, cancel := context.WithTimeout(parent, time.Second)
			defer cancel()
			client, connectErr := Connect(probe, Options{StateDir: dir, Demo: opts.Demo})
			if connectErr != nil {
				return errors.Join(err, connectErr)
			}
			defer client.Close()
			return fmt.Errorf("%w at %s", ErrRunning, client.Meta.URL)
		}
		return err
	}
	defer lock.Close()
	host := u.Hostname()
	if host == "localhost" {
		host = "127.0.0.1"
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	httpListener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("listen for MCP HTTP: %w", err)
	}
	defer httpListener.Close()
	u.Host = httpListener.Addr().String()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	output := opts.Stderr
	if output == nil {
		output = os.Stderr
	}
	audit := newAudit(output)
	audit.event("mcp.started", "url", u.String(), "result", "completed")
	route := newRouter(dir, audit)
	route.ctx = ctx
	server := mcpserver.New(opts.Version, route)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true, PropagateRequestCancellation: true, SessionTimeout: 30 * time.Minute, MaxRequestBodyBytes: 1 << 20})
	protected := http.NewCrossOriginProtection().Handler(handler)
	var requests sync.WaitGroup
	var mu sync.Mutex
	stopping := false
	httpServer := &http.Server{ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }, Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		if stopping {
			mu.Unlock()
			http.Error(w, "MCP daemon stopping", http.StatusServiceUnavailable)
			return
		}
		requests.Add(1)
		mu.Unlock()
		defer requests.Done()
		if req.URL.Path != u.Path {
			http.NotFound(w, req)
			return
		}
		protected.ServeHTTP(w, req)
	})}
	meta := metadata{Protocol: protocolVersion, Address: listener.Addr().String(), Token: randomToken(), PID: os.Getpid(), Demo: opts.Demo, Version: opts.Version, URL: u.String()}
	if err := writeMetadata(dir, meta); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(dir, "endpoint.json"))
	discoveryDone := make(chan struct{})
	go func() { defer close(discoveryDone); route.run(ctx) }()
	rpcDone := make(chan error, 1)
	go func() { rpcDone <- serveRPC(ctx, listener, meta.Token, route, cancel, true) }()
	httpDone := make(chan error, 1)
	go func() { httpDone <- httpServer.Serve(httpListener) }()
	var runErr error
	httpReturned := false
	select {
	case <-ctx.Done():
	case err := <-httpDone:
		httpReturned = true
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}
	cancel()
	mu.Lock()
	stopping = true
	mu.Unlock()
	_ = httpServer.Close()
	requests.Wait()
	for session := range server.Sessions() {
		_ = session.Close()
	}
	if !httpReturned {
		err := <-httpDone
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = errors.Join(runErr, err)
		}
	}
	runErr = errors.Join(runErr, <-rpcDone)
	<-discoveryDone
	audit.event("mcp.stopped", "url", u.String(), "result", "completed")
	return runErr
}
