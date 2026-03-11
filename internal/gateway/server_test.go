package gateway

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Melon-cream/mcp-estuary/internal/mcp"
)

type fakeManager struct {
	tools []mcp.Tool
	call  mcp.CallToolResult
	args  map[string]any
}

func (f *fakeManager) ListTools(context.Context) ([]mcp.Tool, error) {
	return f.tools, nil
}

func (f *fakeManager) CallTool(_ context.Context, _ string, args map[string]any) (mcp.CallToolResult, error) {
	f.args = args
	return f.call, nil
}

func (f *fakeManager) Stats() map[string]any {
	return map[string]any{"configuredServers": len(f.tools)}
}

func (f *fakeManager) StopAll(context.Context) error {
	return nil
}

func TestInitializeAndToolFlow(t *testing.T) {
	manager := &fakeManager{
		tools: []mcp.Tool{{Name: "fetch__read", Title: "fetch__read"}},
		call:  mcp.CallToolResult{Content: []map[string]any{{"type": "text", "text": "ok"}}},
	}
	server := NewServer(log.New(io.Discard, "", 0), manager)
	handler := server.Handler()

	initReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"test","version":"1.0.0"},"capabilities":{}}}`))
	initRec := httptest.NewRecorder()
	handler.ServeHTTP(initRec, initReq)
	if initRec.Code != http.StatusOK {
		t.Fatalf("initialize status=%d body=%s", initRec.Code, initRec.Body.String())
	}

	sessionID := initRec.Header().Get("MCP-Session-Id")
	if sessionID == "" {
		t.Fatal("missing session header")
	}

	initializedReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	initializedReq.Header.Set("MCP-Session-Id", sessionID)
	initializedRec := httptest.NewRecorder()
	handler.ServeHTTP(initializedRec, initializedReq)
	if initializedRec.Code != http.StatusAccepted {
		t.Fatalf("initialized status=%d body=%s", initializedRec.Code, initializedRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	listReq.Header.Set("MCP-Session-Id", sessionID)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("tools/list status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	if !bytes.Contains(listRec.Body.Bytes(), []byte(`"name":"fetch__read"`)) {
		t.Fatalf("unexpected tools/list body: %s", listRec.Body.String())
	}

	callReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fetch__read","arguments":{"url":"https://example.com"}}}`))
	callReq.Header.Set("MCP-Session-Id", sessionID)
	callRec := httptest.NewRecorder()
	handler.ServeHTTP(callRec, callReq)
	if callRec.Code != http.StatusOK {
		t.Fatalf("tools/call status=%d body=%s", callRec.Code, callRec.Body.String())
	}
	if !bytes.Contains(callRec.Body.Bytes(), []byte(`"text":"ok"`)) {
		t.Fatalf("unexpected tools/call body: %s", callRec.Body.String())
	}
}

func TestCallToolWithoutArgumentsUsesEmptyObject(t *testing.T) {
	manager := &fakeManager{
		tools: []mcp.Tool{{Name: "memory__read_graph", Title: "memory__read_graph"}},
		call:  mcp.CallToolResult{Content: []map[string]any{{"type": "text", "text": "ok"}}},
	}
	server := NewServer(log.New(io.Discard, "", 0), manager)
	handler := server.Handler()

	initReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"test","version":"1.0.0"},"capabilities":{}}}`))
	initRec := httptest.NewRecorder()
	handler.ServeHTTP(initRec, initReq)
	sessionID := initRec.Header().Get("MCP-Session-Id")

	initializedReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	initializedReq.Header.Set("MCP-Session-Id", sessionID)
	initializedRec := httptest.NewRecorder()
	handler.ServeHTTP(initializedRec, initializedReq)

	callReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory__read_graph"}}`))
	callReq.Header.Set("MCP-Session-Id", sessionID)
	callRec := httptest.NewRecorder()
	handler.ServeHTTP(callRec, callReq)
	if callRec.Code != http.StatusOK {
		t.Fatalf("tools/call status=%d body=%s", callRec.Code, callRec.Body.String())
	}
	if manager.args == nil {
		t.Fatal("expected empty arguments map")
	}
	if len(manager.args) != 0 {
		t.Fatalf("expected empty arguments map, got %+v", manager.args)
	}
}
