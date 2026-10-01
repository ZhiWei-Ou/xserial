package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ZhiWei-Ou/xserial/internal/debugsession"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type testCaller struct{}

func (testCaller) Call(_ context.Context, op string, input, output any) error {
	if op == "send" {
		out := output.(*debugsession.SendResult)
		*out = debugsession.SendResult{SessionID: "s", Cursor: "s:0", Delivery: "unknown"}
		return &debugsession.Fault{Code: "device_error", Message: "partial write"}
	}
	if op == "read" {
		out := output.(*debugsession.ReadResult)
		*out = debugsession.ReadResult{SessionID: "s", Output: "root@board:~# ", NextCursor: "s:14", Reason: "idle", State: "connected"}
	}
	return nil
}

func TestMCPToolsSchemasResultsAndWriteFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	server := New("test", testCaller{})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "coding-agent", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 6 {
		t.Fatalf("tools = %d", len(tools.Tools))
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "serial_read", Arguments: map[string]any{"session_id": "s", "wait_ms": 0}})
	if err != nil || result.IsError {
		t.Fatalf("read result = %+v, %v", result, err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var read debugsession.ReadResult
	if err := json.Unmarshal(data, &read); err != nil {
		t.Fatal(err)
	}
	if read.Output != "root@board:~# " || read.NextCursor != "s:14" {
		t.Fatalf("read = %+v", read)
	}
	failed, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "serial_send", Arguments: map[string]any{"session_id": "s", "data": "command\n"}})
	if err != nil || !failed.IsError {
		t.Fatalf("send failure = %+v, %v", failed, err)
	}
	data, _ = json.Marshal(failed.StructuredContent)
	var sent debugsession.SendResult
	if err := json.Unmarshal(data, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Cursor != "s:0" || sent.Delivery != "unknown" {
		t.Fatalf("send failure lost checkpoint: %+v", sent)
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "serial_read", Arguments: map[string]any{}})
	if err == nil && !invalid.IsError {
		t.Fatal("missing session_id accepted")
	}
}
