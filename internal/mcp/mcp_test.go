package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/parse"
	"pgregory.net/rapid"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// openTestStore opens an in-memory SQLite store and runs Migrate.
func openTestStore(t *testing.T) *graph.Store {
	t.Helper()
	s, err := graph.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// insertProject inserts a minimal project row.
func insertProject(t *testing.T, s *graph.Store, id string) {
	t.Helper()
	if err := s.UpsertProject(id, id, "/"+id, "go"); err != nil {
		t.Fatalf("UpsertProject %s: %v", id, err)
	}
}

// insertSymbol inserts a single symbol into the store.
func insertSymbol(t *testing.T, s *graph.Store, sym parse.Symbol) {
	t.Helper()
	if err := s.UpsertSymbols([]parse.Symbol{sym}); err != nil {
		t.Fatalf("UpsertSymbols: %v", err)
	}
}

// insertEdge inserts a single edge into the store.
func insertEdge(t *testing.T, s *graph.Store, e parse.Edge) {
	t.Helper()
	if err := s.UpsertEdges([]parse.Edge{e}); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}
}

// postRPC sends a JSON-RPC request directly to srv.handleMCP and returns the
// decoded Response. Because the test is in package mcp (white-box), we can
// call the unexported method directly.
func postRPC(t *testing.T, srv *Server, req Request) Response {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleMCP(w, r)
	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// toolCallReq builds a tools/call Request for the given tool name and arguments.
func toolCallReq(id interface{}, toolName string, args map[string]interface{}) Request {
	params, _ := json.Marshal(map[string]interface{}{
		"name":      toolName,
		"arguments": args,
	})
	return Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  json.RawMessage(params),
	}
}

// decodeToolCallResult unmarshals Response.Result into a ToolCallResult.
func decodeToolCallResult(t *testing.T, resp Response) ToolCallResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}
	b, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var tcr ToolCallResult
	if err := json.Unmarshal(b, &tcr); err != nil {
		t.Fatalf("unmarshal ToolCallResult: %v", err)
	}
	return tcr
}

// makeSymbol is a convenience constructor for test symbols.
func makeSymbol(id, name string, kind parse.SymbolKind, projectID string) parse.Symbol {
	return parse.Symbol{
		ID:        id,
		Name:      name,
		Kind:      kind,
		File:      "/" + projectID + "/" + name + ".go",
		StartLine: 1,
		EndLine:   10,
		Signature: name + "()",
		ProjectID: projectID,
	}
}

// ─── unit tests ───────────────────────────────────────────────────────────────

// 1. TestInitializeHandshake verifies the initialize response shape.
func TestInitializeHandshake(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	b, _ := json.Marshal(resp.Result)
	var result InitializeResult
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

// 2. TestToolsList verifies exactly 9 tools with the expected names.
func TestToolsList(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	})

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	b, _ := json.Marshal(resp.Result)
	var result ToolsListResult
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

// 3. TestToolCall_Search inserts a symbol and verifies it appears in search results.
func TestToolCall_Search(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")
	sym := makeSymbol("sym-search-001", "UniqueSearchFunc", parse.KindFunction, "proj1")
	insertSymbol(t, s, sym)

	srv := NewServer(s, "127.0.0.1:0")
	resp := postRPC(t, srv, toolCallReq(3, "codegraph/search", map[string]interface{}{
		"name": "UniqueSearchFunc",
	}))

	tcr := decodeToolCallResult(t, resp)
	if len(tcr.Content) == 0 {
		t.Fatal("content is empty")
	}
	if tcr.Content[0].Type != "text" {
		t.Errorf("content[0].type = %q, want %q", tcr.Content[0].Type, "text")
	}
	if !strings.Contains(tcr.Content[0].Text, "UniqueSearchFunc") {
		t.Errorf("result text does not contain symbol name; got: %s", tcr.Content[0].Text)
	}
}

// 4. TestToolCall_Callers inserts A->B calls edge and verifies A is in callers of B.
func TestToolCall_Callers(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")
	symA := makeSymbol("caller-A", "FuncA", parse.KindFunction, "proj1")
	symB := makeSymbol("caller-B", "FuncB", parse.KindFunction, "proj1")
	insertSymbol(t, s, symA)
	insertSymbol(t, s, symB)
	insertEdge(t, s, parse.Edge{FromID: "caller-A", ToID: "caller-B", Kind: parse.EdgeCalls, File: "/proj1/a.go", Line: 5})

	srv := NewServer(s, "127.0.0.1:0")
	resp := postRPC(t, srv, toolCallReq(4, "codegraph/callers", map[string]interface{}{
		"symbolId": "caller-B",
	}))

	tcr := decodeToolCallResult(t, resp)
	if len(tcr.Content) == 0 {
		t.Fatal("content is empty")
	}
	if !strings.Contains(tcr.Content[0].Text, "caller-A") {
		t.Errorf("callers result does not contain caller-A; got: %s", tcr.Content[0].Text)
	}
}

// 5. TestToolCall_Callees inserts A->B calls edge and verifies B is in callees of A.
func TestToolCall_Callees(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")
	symA := makeSymbol("callee-A", "FuncA", parse.KindFunction, "proj1")
	symB := makeSymbol("callee-B", "FuncB", parse.KindFunction, "proj1")
	insertSymbol(t, s, symA)
	insertSymbol(t, s, symB)
	insertEdge(t, s, parse.Edge{FromID: "callee-A", ToID: "callee-B", Kind: parse.EdgeCalls, File: "/proj1/a.go", Line: 5})

	srv := NewServer(s, "127.0.0.1:0")
	resp := postRPC(t, srv, toolCallReq(5, "codegraph/callees", map[string]interface{}{
		"symbolId": "callee-A",
	}))

	tcr := decodeToolCallResult(t, resp)
	if len(tcr.Content) == 0 {
		t.Fatal("content is empty")
	}
	if !strings.Contains(tcr.Content[0].Text, "callee-B") {
		t.Errorf("callees result does not contain callee-B; got: %s", tcr.Content[0].Text)
	}
}

// 6. TestToolCall_Dependencies inserts A->B imports edge and verifies B is in dependencies of A.
func TestToolCall_Dependencies(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")
	symA := makeSymbol("dep-A", "ModA", parse.KindModule, "proj1")
	symB := makeSymbol("dep-B", "ModB", parse.KindModule, "proj1")
	insertSymbol(t, s, symA)
	insertSymbol(t, s, symB)
	insertEdge(t, s, parse.Edge{FromID: "dep-A", ToID: "dep-B", Kind: parse.EdgeImports, File: "/proj1/a.go", Line: 1})

	srv := NewServer(s, "127.0.0.1:0")
	resp := postRPC(t, srv, toolCallReq(6, "codegraph/dependencies", map[string]interface{}{
		"symbolId": "dep-A",
	}))

	tcr := decodeToolCallResult(t, resp)
	if len(tcr.Content) == 0 {
		t.Fatal("content is empty")
	}
	if !strings.Contains(tcr.Content[0].Text, "dep-B") {
		t.Errorf("dependencies result does not contain dep-B; got: %s", tcr.Content[0].Text)
	}
}

// 7. TestToolCall_Dependents inserts A->B imports edge and verifies A is in dependents of B.
func TestToolCall_Dependents(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")
	symA := makeSymbol("dent-A", "ModA", parse.KindModule, "proj1")
	symB := makeSymbol("dent-B", "ModB", parse.KindModule, "proj1")
	insertSymbol(t, s, symA)
	insertSymbol(t, s, symB)
	insertEdge(t, s, parse.Edge{FromID: "dent-A", ToID: "dent-B", Kind: parse.EdgeImports, File: "/proj1/a.go", Line: 1})

	srv := NewServer(s, "127.0.0.1:0")
	resp := postRPC(t, srv, toolCallReq(7, "codegraph/dependents", map[string]interface{}{
		"symbolId": "dent-B",
	}))

	tcr := decodeToolCallResult(t, resp)
	if len(tcr.Content) == 0 {
		t.Fatal("content is empty")
	}
	if !strings.Contains(tcr.Content[0].Text, "dent-A") {
		t.Errorf("dependents result does not contain dent-A; got: %s", tcr.Content[0].Text)
	}
}

// 8. TestToolCall_Implementors inserts A implements B edge and verifies A is in implementors of B.
func TestToolCall_Implementors(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")
	symA := makeSymbol("impl-A", "ConcreteType", parse.KindType, "proj1")
	symB := makeSymbol("impl-B", "MyInterface", parse.KindInterface, "proj1")
	insertSymbol(t, s, symA)
	insertSymbol(t, s, symB)
	insertEdge(t, s, parse.Edge{FromID: "impl-A", ToID: "impl-B", Kind: parse.EdgeImplements, File: "/proj1/a.go", Line: 1})

	srv := NewServer(s, "127.0.0.1:0")
	resp := postRPC(t, srv, toolCallReq(8, "codegraph/implementors", map[string]interface{}{
		"symbolId": "impl-B",
	}))

	tcr := decodeToolCallResult(t, resp)
	if len(tcr.Content) == 0 {
		t.Fatal("content is empty")
	}
	if !strings.Contains(tcr.Content[0].Text, "impl-A") {
		t.Errorf("implementors result does not contain impl-A; got: %s", tcr.Content[0].Text)
	}
}

// 9. TestToolCall_Grep inserts a symbol with a distinctive name and verifies grep finds it.
func TestToolCall_Grep(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")
	sym := makeSymbol("grep-001", "XyzzyDistinctiveName", parse.KindFunction, "proj1")
	insertSymbol(t, s, sym)

	srv := NewServer(s, "127.0.0.1:0")
	resp := postRPC(t, srv, toolCallReq(9, "codegraph/grep", map[string]interface{}{
		"query": "XyzzyDistinctiveName",
	}))

	tcr := decodeToolCallResult(t, resp)
	if len(tcr.Content) == 0 {
		t.Fatal("content is empty")
	}
	if tcr.Content[0].Type != "text" {
		t.Errorf("content[0].type = %q, want %q", tcr.Content[0].Type, "text")
	}
	if !strings.Contains(tcr.Content[0].Text, "XyzzyDistinctiveName") {
		t.Errorf("grep result does not contain symbol name; got: %s", tcr.Content[0].Text)
	}
}

// 10. TestToolCall_Symbol inserts a symbol and verifies codegraph/symbol returns its details.
func TestToolCall_Symbol(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")
	sym := makeSymbol("sym-detail-001", "DetailFunc", parse.KindFunction, "proj1")
	insertSymbol(t, s, sym)

	srv := NewServer(s, "127.0.0.1:0")
	resp := postRPC(t, srv, toolCallReq(10, "codegraph/symbol", map[string]interface{}{
		"symbolId": "sym-detail-001",
	}))

	tcr := decodeToolCallResult(t, resp)
	if len(tcr.Content) == 0 {
		t.Fatal("content is empty")
	}
	if !strings.Contains(tcr.Content[0].Text, "DetailFunc") {
		t.Errorf("symbol result does not contain symbol name; got: %s", tcr.Content[0].Text)
	}
}

// 11. TestToolCall_Stats verifies codegraph/stats returns a JSON array.
func TestToolCall_Stats(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "stats-proj")
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, toolCallReq(11, "codegraph/stats", map[string]interface{}{}))

	tcr := decodeToolCallResult(t, resp)
	if len(tcr.Content) == 0 {
		t.Fatal("content is empty")
	}
	text := strings.TrimSpace(tcr.Content[0].Text)
	// GetProjectStats returns a nil slice when there are no projects, which
	// marshals to JSON "null". Both "null" and "[...]" are valid representations
	// of an empty/absent array.
	isArray := (strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]"))
	isNull := text == "null"
	if !isArray && !isNull {
		t.Errorf("stats result is not a JSON array or null; got: %s", text)
	}
}

// 12. TestUnknownTool_Returns32601 verifies that calling an unknown tool returns -32601.
func TestUnknownTool_Returns32601(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, toolCallReq(12, "codegraph/unknown", map[string]interface{}{}))

	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != ErrMethodNotFound {
		t.Errorf("error.code = %d, want %d", resp.Error.Code, ErrMethodNotFound)
	}
}

// 13. TestMissingRequiredArg_Returns32602 verifies that omitting symbolId returns -32602.
func TestMissingRequiredArg_Returns32602(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, toolCallReq(13, "codegraph/callers", map[string]interface{}{}))

	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != ErrInvalidParams {
		t.Errorf("error.code = %d, want %d", resp.Error.Code, ErrInvalidParams)
	}
}

// 14. TestMissingRequiredArg_Grep_Returns32602 verifies that omitting query returns -32602.
func TestMissingRequiredArg_Grep_Returns32602(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, toolCallReq(14, "codegraph/grep", map[string]interface{}{}))

	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != ErrInvalidParams {
		t.Errorf("error.code = %d, want %d", resp.Error.Code, ErrInvalidParams)
	}
}

// 15. TestOutOfRangeDepth_Returns32602 verifies depth=0 and depth=11 both return -32602.
func TestOutOfRangeDepth_Returns32602(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	for _, depth := range []float64{0, 11} {
		resp := postRPC(t, srv, toolCallReq(15, "codegraph/callers", map[string]interface{}{
			"symbolId": "some-id",
			"depth":    depth,
		}))
		if resp.Error == nil {
			t.Errorf("depth=%v: expected error, got nil", depth)
			continue
		}
		if resp.Error.Code != ErrInvalidParams {
			t.Errorf("depth=%v: error.code = %d, want %d", depth, resp.Error.Code, ErrInvalidParams)
		}
	}
}

// 16. TestOutOfRangeLimit_Returns32602 verifies limit=0 and limit=101 both return -32602.
func TestOutOfRangeLimit_Returns32602(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	for _, limit := range []float64{0, 101} {
		resp := postRPC(t, srv, toolCallReq(16, "codegraph/grep", map[string]interface{}{
			"query": "test",
			"limit": limit,
		}))
		if resp.Error == nil {
			t.Errorf("limit=%v: expected error, got nil", limit)
			continue
		}
		if resp.Error.Code != ErrInvalidParams {
			t.Errorf("limit=%v: error.code = %d, want %d", limit, resp.Error.Code, ErrInvalidParams)
		}
	}
}

// 17. TestWrongTypeArg_Returns32602 verifies that passing symbolId as a number returns -32602.
func TestWrongTypeArg_Returns32602(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, toolCallReq(17, "codegraph/callers", map[string]interface{}{
		"symbolId": float64(123),
	}))

	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != ErrInvalidParams {
		t.Errorf("error.code = %d, want %d", resp.Error.Code, ErrInvalidParams)
	}
}

// 18. TestUnknownMethod_Returns32601 verifies that an unknown JSON-RPC method returns -32601.
func TestUnknownMethod_Returns32601(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, Request{
		JSONRPC: "2.0",
		ID:      18,
		Method:  "unknown/method",
	})

	if resp.Error == nil {
		t.Fatal("expected error, got nil")
	}
	if resp.Error.Code != ErrMethodNotFound {
		t.Errorf("error.code = %d, want %d", resp.Error.Code, ErrMethodNotFound)
	}
}

// 19. TestDefaultDepth_Callers verifies that omitting depth uses the default (3) without error.
func TestDefaultDepth_Callers(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, toolCallReq(19, "codegraph/callers", map[string]interface{}{
		"symbolId": "nonexistent-id",
	}))

	if resp.Error != nil {
		t.Errorf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}
}

// 20. TestDefaultLimit_Grep verifies that omitting limit uses the default (20) without error.
func TestDefaultLimit_Grep(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	resp := postRPC(t, srv, toolCallReq(20, "codegraph/grep", map[string]interface{}{
		"query": "test",
	}))

	if resp.Error != nil {
		t.Errorf("unexpected error: code=%d msg=%s", resp.Error.Code, resp.Error.Message)
	}
}

// ─── property-based tests ─────────────────────────────────────────────────────

// allToolNames is the canonical list of the nine codegraph tools.
var allToolNames = []string{
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

// symbolIdTools is the subset of tools that require a symbolId argument.
var symbolIdTools = []string{
	"codegraph/callers",
	"codegraph/callees",
	"codegraph/dependencies",
	"codegraph/dependents",
	"codegraph/implementors",
	"codegraph/symbol",
}

// minimalValidArgs returns the minimal valid arguments for each tool so that
// required parameters are satisfied. The store may return empty results, but
// no validation error should occur.
func minimalValidArgs(toolName string) map[string]interface{} {
	switch toolName {
	case "codegraph/callers", "codegraph/callees",
		"codegraph/dependencies", "codegraph/dependents",
		"codegraph/implementors", "codegraph/symbol":
		return map[string]interface{}{"symbolId": "nonexistent-id"}
	case "codegraph/grep":
		return map[string]interface{}{"query": "test"}
	default:
		// codegraph/search and codegraph/stats have no required args.
		return map[string]interface{}{}
	}
}

// 21. TestProperty_ValidToolCallReturnsContent verifies that for any valid tool
// with minimal valid arguments, the response has no error and content[0].type="text".
func TestProperty_ValidToolCallReturnsContent(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	rapid.Check(t, func(rt *rapid.T) {
		idx := rapid.IntRange(0, len(allToolNames)-1).Draw(rt, "toolIdx")
		toolName := allToolNames[idx]
		args := minimalValidArgs(toolName)

		resp := postRPC(t, srv, toolCallReq("prop-1", toolName, args))

		if resp.Error != nil {
			rt.Fatalf("tool %q returned unexpected error: code=%d msg=%s",
				toolName, resp.Error.Code, resp.Error.Message)
		}

		b, err := json.Marshal(resp.Result)
		if err != nil {
			rt.Fatalf("re-marshal result: %v", err)
		}
		var tcr ToolCallResult
		if err := json.Unmarshal(b, &tcr); err != nil {
			rt.Fatalf("unmarshal ToolCallResult: %v", err)
		}
		if len(tcr.Content) == 0 {
			rt.Fatalf("tool %q returned empty content", toolName)
		}
		if tcr.Content[0].Type != "text" {
			rt.Fatalf("tool %q: content[0].type = %q, want %q",
				toolName, tcr.Content[0].Type, "text")
		}
	})
}

// 22. TestProperty_InvalidArgsReturns32602 verifies that passing symbolId as a
// float64 (instead of string) always returns error code -32602.
func TestProperty_InvalidArgsReturns32602(t *testing.T) {
	s := openTestStore(t)
	srv := NewServer(s, "127.0.0.1:0")

	rapid.Check(t, func(rt *rapid.T) {
		idx := rapid.IntRange(0, len(symbolIdTools)-1).Draw(rt, "toolIdx")
		toolName := symbolIdTools[idx]
		// Pass symbolId as a number instead of a string to trigger type validation.
		numericID := rapid.Float64().Draw(rt, "numericId")
		args := map[string]interface{}{
			"symbolId": numericID,
		}

		resp := postRPC(t, srv, toolCallReq("prop-2", toolName, args))

		if resp.Error == nil {
			rt.Fatalf("tool %q with numeric symbolId: expected error, got nil", toolName)
		}
		if resp.Error.Code != ErrInvalidParams {
			rt.Fatalf("tool %q with numeric symbolId: error.code = %d, want %d",
				toolName, resp.Error.Code, ErrInvalidParams)
		}
	})
}
