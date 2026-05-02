package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/mcp"
	"github.com/codegraph-cli/codegraph/internal/parse"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// openE2EStore opens an in-memory SQLite store, runs Migrate, and seeds it
// with a minimal project and a handful of symbols/edges so the tool handlers
// have something to return.
func openE2EStore(t *testing.T) *graph.Store {
	t.Helper()
	s, err := graph.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Seed a project.
	if err := s.UpsertProject("e2e-proj", "e2e-proj", "/e2e", "go"); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	// Seed symbols.
	symbols := []parse.Symbol{
		{ID: "e2e-sym-A", Name: "FuncA", Kind: parse.KindFunction, File: "/e2e/a.go", StartLine: 1, EndLine: 5, Signature: "FuncA()", ProjectID: "e2e-proj"},
		{ID: "e2e-sym-B", Name: "FuncB", Kind: parse.KindFunction, File: "/e2e/a.go", StartLine: 7, EndLine: 12, Signature: "FuncB()", ProjectID: "e2e-proj"},
		{ID: "e2e-iface", Name: "MyInterface", Kind: parse.KindInterface, File: "/e2e/iface.go", StartLine: 1, EndLine: 5, Signature: "MyInterface", ProjectID: "e2e-proj"},
		{ID: "e2e-impl", Name: "MyImpl", Kind: parse.KindType, File: "/e2e/impl.go", StartLine: 1, EndLine: 10, Signature: "MyImpl", ProjectID: "e2e-proj"},
		{ID: "e2e-mod-A", Name: "ModA", Kind: parse.KindModule, File: "/e2e/mod_a.go", StartLine: 1, EndLine: 3, Signature: "ModA", ProjectID: "e2e-proj"},
		{ID: "e2e-mod-B", Name: "ModB", Kind: parse.KindModule, File: "/e2e/mod_b.go", StartLine: 1, EndLine: 3, Signature: "ModB", ProjectID: "e2e-proj"},
	}
	if err := s.UpsertSymbols(symbols); err != nil {
		t.Fatalf("UpsertSymbols: %v", err)
	}

	// Seed edges.
	edges := []parse.Edge{
		{FromID: "e2e-sym-A", ToID: "e2e-sym-B", Kind: parse.EdgeCalls, File: "/e2e/a.go", Line: 3},
		{FromID: "e2e-impl", ToID: "e2e-iface", Kind: parse.EdgeImplements, File: "/e2e/impl.go", Line: 1},
		{FromID: "e2e-mod-A", ToID: "e2e-mod-B", Kind: parse.EdgeImports, File: "/e2e/mod_a.go", Line: 1},
	}
	if err := s.UpsertEdges(edges); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	return s
}

// startE2EServer starts an MCP server on a random port and returns the base
// URL. The server is stopped when the test ends.
func startE2EServer(t *testing.T, store *graph.Store) string {
	t.Helper()

	// Pick a random free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := mcp.NewServer(store, addr)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Wait for the server to be ready.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Stop(ctx) //nolint:errcheck
	})

	return fmt.Sprintf("http://%s/mcp", addr)
}

// postMCP sends a JSON-RPC 2.0 POST request to url and returns the decoded
// Response.
func postMCP(t *testing.T, url string, req mcp.Request) mcp.Response {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()

	var rpcResp mcp.Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return rpcResp
}

// toolCallReq builds a tools/call Request for the given tool name and arguments.
func toolCallReq(id interface{}, toolName string, args map[string]interface{}) mcp.Request {
	params, _ := json.Marshal(map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	})
	return mcp.Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  json.RawMessage(params),
	}
}

// assertContent verifies that the response has no error and contains at least
// one content item of type "text".
func assertContent(t *testing.T, resp mcp.Response, toolName string) mcp.ToolCallResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("tool %q returned unexpected RPC error: code=%d msg=%s",
			toolName, resp.Error.Code, resp.Error.Message)
	}
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var tcr mcp.ToolCallResult
	if err := json.Unmarshal(b, &tcr); err != nil {
		t.Fatalf("unmarshal ToolCallResult: %v", err)
	}
	if len(tcr.Content) == 0 {
		t.Fatalf("tool %q: content is empty", toolName)
	}
	if tcr.Content[0].Type != "text" {
		t.Errorf("tool %q: content[0].type = %q, want %q", toolName, tcr.Content[0].Type, "text")
	}
	return tcr
}

// ─── E2E tests ────────────────────────────────────────────────────────────────

// TestE2E_MCP_AllNineTools starts a real MCP HTTP server and sends one
// tools/call request for each of the nine tools, verifying that every response
// has a valid JSON-RPC 2.0 structure with a non-empty content field.
func TestE2E_MCP_AllNineTools(t *testing.T) {
	store := openE2EStore(t)
	url := startE2EServer(t, store)

	tests := []struct {
		name string
		req  mcp.Request
	}{
		{
			name: "codegraph/search",
			req:  toolCallReq(1, "codegraph/search", map[string]interface{}{"name": "FuncA"}),
		},
		{
			name: "codegraph/callers",
			req:  toolCallReq(2, "codegraph/callers", map[string]interface{}{"symbolId": "e2e-sym-B"}),
		},
		{
			name: "codegraph/callees",
			req:  toolCallReq(3, "codegraph/callees", map[string]interface{}{"symbolId": "e2e-sym-A"}),
		},
		{
			name: "codegraph/dependencies",
			req:  toolCallReq(4, "codegraph/dependencies", map[string]interface{}{"symbolId": "e2e-mod-A"}),
		},
		{
			name: "codegraph/dependents",
			req:  toolCallReq(5, "codegraph/dependents", map[string]interface{}{"symbolId": "e2e-mod-B"}),
		},
		{
			name: "codegraph/implementors",
			req:  toolCallReq(6, "codegraph/implementors", map[string]interface{}{"symbolId": "e2e-iface"}),
		},
		{
			name: "codegraph/grep",
			req:  toolCallReq(7, "codegraph/grep", map[string]interface{}{"query": "FuncA"}),
		},
		{
			name: "codegraph/symbol",
			req:  toolCallReq(8, "codegraph/symbol", map[string]interface{}{"symbolId": "e2e-sym-A"}),
		},
		{
			name: "codegraph/stats",
			req:  toolCallReq(9, "codegraph/stats", map[string]interface{}{}),
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := postMCP(t, url, tc.req)

			// Every response must carry the JSON-RPC 2.0 version string.
			if resp.JSONRPC != "2.0" {
				t.Errorf("jsonrpc = %q, want %q", resp.JSONRPC, "2.0")
			}

			// Verify content field is present and well-formed.
			assertContent(t, resp, tc.name)
		})
	}
}

// TestE2E_MCP_Initialize verifies the initialize handshake over a real HTTP
// connection.
func TestE2E_MCP_Initialize(t *testing.T) {
	store := openE2EStore(t)
	url := startE2EServer(t, store)

	resp := postMCP(t, url, mcp.Request{
		JSONRPC: "2.0",
		ID:      100,
		Method:  "initialize",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", resp.JSONRPC, "2.0")
	}

	b, _ := json.Marshal(resp.Result)
	var result mcp.InitializeResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal InitializeResult: %v", err)
	}
	if result.ProtocolVersion == "" {
		t.Error("protocolVersion is empty")
	}
	if result.ServerInfo.Name != "codegraph" {
		t.Errorf("serverInfo.name = %q, want %q", result.ServerInfo.Name, "codegraph")
	}
	if result.Capabilities.Tools == nil {
		t.Error("capabilities.tools is nil")
	}
}

// TestE2E_MCP_ToolsList verifies the tools/list response over a real HTTP
// connection returns exactly nine tools.
func TestE2E_MCP_ToolsList(t *testing.T) {
	store := openE2EStore(t)
	url := startE2EServer(t, store)

	resp := postMCP(t, url, mcp.Request{
		JSONRPC: "2.0",
		ID:      101,
		Method:  "tools/list",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}

	b, _ := json.Marshal(resp.Result)
	var result mcp.ToolsListResult
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal ToolsListResult: %v", err)
	}

	wantNames := []string{
		"codegraph/search",
		"codegraph/callers",
		"codegraph/callees",
		"codegraph/dependencies",
		"codegraph/dependents",
		"codegraph/implementors",
		"codegraph/grep",
		"codegraph/symbol",
		"codegraph/stats",
	}

	if len(result.Tools) != len(wantNames) {
		t.Fatalf("got %d tools, want %d", len(result.Tools), len(wantNames))
	}

	nameSet := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		nameSet[tool.Name] = true
	}
	for _, name := range wantNames {
		if !nameSet[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

// TestE2E_MCP_UnknownTool verifies that an unknown tool returns -32601 over a
// real HTTP connection.
func TestE2E_MCP_UnknownTool(t *testing.T) {
	store := openE2EStore(t)
	url := startE2EServer(t, store)

	resp := postMCP(t, url, toolCallReq(200, "codegraph/nonexistent", map[string]interface{}{}))

	if resp.Error == nil {
		t.Fatal("expected RPC error for unknown tool, got nil")
	}
	if resp.Error.Code != mcp.ErrMethodNotFound {
		t.Errorf("error.code = %d, want %d (ErrMethodNotFound)", resp.Error.Code, mcp.ErrMethodNotFound)
	}
}

// TestE2E_MCP_InvalidParams verifies that missing required args return -32602
// over a real HTTP connection.
func TestE2E_MCP_InvalidParams(t *testing.T) {
	store := openE2EStore(t)
	url := startE2EServer(t, store)

	// codegraph/callers requires symbolId.
	resp := postMCP(t, url, toolCallReq(201, "codegraph/callers", map[string]interface{}{}))

	if resp.Error == nil {
		t.Fatal("expected RPC error for missing symbolId, got nil")
	}
	if resp.Error.Code != mcp.ErrInvalidParams {
		t.Errorf("error.code = %d, want %d (ErrInvalidParams)", resp.Error.Code, mcp.ErrInvalidParams)
	}
}
