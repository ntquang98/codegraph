package ui_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/parse"
	"github.com/codegraph-cli/codegraph/internal/ui"
	"net/http/httptest"
)

// ─── E2E helpers ─────────────────────────────────────────────────────────────

// e2eStore is a mock Store pre-populated with representative data so every
// REST endpoint has something to return.
type e2eStore struct {
	symbols   []parse.Symbol
	symbol    *parse.Symbol
	callers   []parse.Symbol
	callees   []parse.Symbol
	stats     []graph.ProjectStats
	ftsResult []parse.Symbol
	files     []string
}

func (m *e2eStore) SearchSymbols(_ graph.SearchQuery) ([]parse.Symbol, error) {
	return m.symbols, nil
}
func (m *e2eStore) CountSymbols(_ graph.SearchQuery) (int, error) {
	return len(m.symbols), nil
}
func (m *e2eStore) GetSymbol(_ string) (*parse.Symbol, error) {
	return m.symbol, nil
}
func (m *e2eStore) GetCallers(_ string, _ int) ([]parse.Symbol, error) {
	return m.callers, nil
}
func (m *e2eStore) GetCallees(_ string, _ int) ([]parse.Symbol, error) {
	return m.callees, nil
}
func (m *e2eStore) GetProjectStats() ([]graph.ProjectStats, error) {
	return m.stats, nil
}
func (m *e2eStore) FullTextSearch(_ string, _ int) ([]parse.Symbol, error) {
	return m.ftsResult, nil
}
func (m *e2eStore) GetAllTrackedFiles() ([]string, error) {
	return m.files, nil
}
func (m *e2eStore) GetAllEdges() ([]parse.Edge, error) {
	return nil, nil
}
func (m *e2eStore) GetEdgesForNodes(_ map[string]struct{}) ([]parse.Edge, error) {
	return nil, nil
}
func (m *e2eStore) GetEdgesFromNodes(_ map[string]struct{}) ([]parse.Edge, error) {
	return nil, nil
}

// newE2EStore returns a pre-populated e2eStore.
func newE2EStore() *e2eStore {
	sym := parse.Symbol{
		ID:        "e2e-ui-sym-1",
		Name:      "HandleRequest",
		Kind:      parse.KindFunction,
		File:      "/app/handler.go",
		StartLine: 10,
		EndLine:   30,
		Signature: "HandleRequest(w http.ResponseWriter, r *http.Request)",
		ProjectID: "app",
	}
	return &e2eStore{
		symbols: []parse.Symbol{sym},
		symbol:  &sym,
		callers: []parse.Symbol{
			{ID: "e2e-ui-caller", Name: "main", Kind: parse.KindFunction, File: "/app/main.go", ProjectID: "app"},
		},
		callees: []parse.Symbol{
			{ID: "e2e-ui-callee", Name: "writeJSON", Kind: parse.KindFunction, File: "/app/util.go", ProjectID: "app"},
		},
		stats: []graph.ProjectStats{
			{ProjectID: "app", SymbolCount: 42, EdgeCount: 15, FileCount: 7},
		},
		ftsResult: []parse.Symbol{sym},
		files:     []string{"/app/handler.go", "/app/main.go"},
	}
}

// newE2EServer creates a UI server backed by the e2eStore and returns an
// httptest.Server. The caller must call ts.Close() when done.
func newE2EServer(t *testing.T) (*httptest.Server, *e2eStore) {
	t.Helper()
	store := newE2EStore()
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><body>Graph UI</body></html>")},
	}
	srv := ui.NewServer(store, "127.0.0.1:0", assets, true /* noOpen */)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, store
}

// getE2E performs a GET request and returns the response and raw body bytes.
func getE2E(t *testing.T, ts *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var buf []byte
	buf = make([]byte, 0, 4096)
	tmp := make([]byte, 512)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return resp, buf
}

// assertStatus checks the HTTP status code.
func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Errorf("status: got %d, want %d", resp.StatusCode, want)
	}
}

// assertJSONContentType checks the Content-Type header.
func assertJSONContentType(t *testing.T, resp *http.Response) {
	t.Helper()
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}
}

// assertCORSHeader checks the CORS header.
func assertCORSHeader(t *testing.T, resp *http.Response) {
	t.Helper()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin: got %q, want %q", got, "*")
	}
}

// ─── E2E tests ────────────────────────────────────────────────────────────────

// TestE2E_UI_GetGraph verifies GET /api/graph returns 200 with the expected
// JSON shape: nodes, edges, total_nodes, total_edges, page, page_size.
func TestE2E_UI_GetGraph(t *testing.T) {
	ts, _ := newE2EServer(t)

	resp, body := getE2E(t, ts, "/api/graph")

	assertStatus(t, resp, http.StatusOK)
	assertJSONContentType(t, resp)
	assertCORSHeader(t, resp)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode /api/graph: %v", err)
	}

	requiredFields := []string{"nodes", "edges", "total_nodes", "total_edges", "page", "page_size"}
	for _, field := range requiredFields {
		if _, ok := result[field]; !ok {
			t.Errorf("/api/graph: missing field %q", field)
		}
	}

	nodes, ok := result["nodes"].([]interface{})
	if !ok {
		t.Fatal("/api/graph: nodes is not an array")
	}
	if len(nodes) == 0 {
		t.Error("/api/graph: expected at least one node")
	}
}

// TestE2E_UI_GetGraph_Pagination verifies that page and page_size query params
// are reflected in the response.
func TestE2E_UI_GetGraph_Pagination(t *testing.T) {
	ts, _ := newE2EServer(t)

	resp, body := getE2E(t, ts, "/api/graph?page=2&page_size=25")

	assertStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode /api/graph: %v", err)
	}

	if page, ok := result["page"].(float64); !ok || int(page) != 2 {
		t.Errorf("page: got %v, want 2", result["page"])
	}
	if ps, ok := result["page_size"].(float64); !ok || int(ps) != 25 {
		t.Errorf("page_size: got %v, want 25", result["page_size"])
	}
}

// TestE2E_UI_GetSymbols verifies GET /api/symbols returns 200 with a JSON
// array of symbol objects.
func TestE2E_UI_GetSymbols(t *testing.T) {
	ts, _ := newE2EServer(t)

	resp, body := getE2E(t, ts, "/api/symbols")

	assertStatus(t, resp, http.StatusOK)
	assertJSONContentType(t, resp)
	assertCORSHeader(t, resp)

	var symbols []interface{}
	if err := json.Unmarshal(body, &symbols); err != nil {
		t.Fatalf("decode /api/symbols: %v", err)
	}
	if len(symbols) == 0 {
		t.Error("/api/symbols: expected at least one symbol")
	}
}

// TestE2E_UI_GetSymbols_WithNameFilter verifies that the name query param is
// accepted without error.
func TestE2E_UI_GetSymbols_WithNameFilter(t *testing.T) {
	ts, _ := newE2EServer(t)

	resp, body := getE2E(t, ts, "/api/symbols?name=HandleRequest")

	assertStatus(t, resp, http.StatusOK)

	var symbols []interface{}
	if err := json.Unmarshal(body, &symbols); err != nil {
		t.Fatalf("decode /api/symbols: %v", err)
	}
	// The mock always returns the seeded symbol regardless of filter.
	if len(symbols) == 0 {
		t.Error("/api/symbols?name=HandleRequest: expected at least one result")
	}
}

// TestE2E_UI_GetSymbolByID verifies GET /api/symbol/:id returns 200 with the
// expected shape: symbol, callers, callees.
func TestE2E_UI_GetSymbolByID(t *testing.T) {
	ts, _ := newE2EServer(t)

	resp, body := getE2E(t, ts, "/api/symbol/e2e-ui-sym-1")

	assertStatus(t, resp, http.StatusOK)
	assertJSONContentType(t, resp)
	assertCORSHeader(t, resp)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode /api/symbol/:id: %v", err)
	}

	requiredFields := []string{"symbol", "callers", "callees"}
	for _, field := range requiredFields {
		if _, ok := result[field]; !ok {
			t.Errorf("/api/symbol/:id: missing field %q", field)
		}
	}

	// Verify the symbol sub-object has an ID field.
	// parse.Symbol has no JSON tags so fields serialize with their Go names.
	symObj, ok := result["symbol"].(map[string]interface{})
	if !ok {
		t.Fatal("/api/symbol/:id: symbol is not an object")
	}
	if symObj["ID"] != "e2e-ui-sym-1" {
		t.Errorf("symbol.ID = %v, want %q", symObj["ID"], "e2e-ui-sym-1")
	}
}

// TestE2E_UI_GetSymbolByID_NotFound verifies that a non-existent symbol ID
// returns 404 with an error field.
func TestE2E_UI_GetSymbolByID_NotFound(t *testing.T) {
	// Use a store that returns nil for GetSymbol to trigger the 404 path.
	nilStore := &e2eStore{
		symbols: []parse.Symbol{},
		symbol:  nil, // GetSymbol returns nil → 404
	}
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html></html>")}}
	srv := ui.NewServer(nilStore, "127.0.0.1:0", assets, true)
	nilTS := httptest.NewServer(srv.Handler())
	defer nilTS.Close()

	resp, body := getE2E(t, nilTS, "/api/symbol/does-not-exist")

	assertStatus(t, resp, http.StatusNotFound)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode 404 body: %v", err)
	}
	if _, ok := result["error"]; !ok {
		t.Error("/api/symbol/does-not-exist: missing error field in 404 response")
	}
}

// TestE2E_UI_GetProjects verifies GET /api/projects returns 200 with a JSON
// array containing project stats objects.
func TestE2E_UI_GetProjects(t *testing.T) {
	ts, _ := newE2EServer(t)

	resp, body := getE2E(t, ts, "/api/projects")

	assertStatus(t, resp, http.StatusOK)
	assertJSONContentType(t, resp)
	assertCORSHeader(t, resp)

	var stats []interface{}
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("decode /api/projects: %v", err)
	}
	if len(stats) == 0 {
		t.Error("/api/projects: expected at least one project stat")
	}

	// Verify the first stat has the expected fields.
	first, ok := stats[0].(map[string]interface{})
	if !ok {
		t.Fatal("/api/projects: first element is not an object")
	}
	for _, field := range []string{"ProjectID", "SymbolCount", "EdgeCount", "FileCount"} {
		if _, ok := first[field]; !ok {
			t.Errorf("/api/projects: missing field %q in project stat", field)
		}
	}
}

// TestE2E_UI_Search verifies GET /api/search?q=... returns 200 with a JSON
// array of matching symbols.
func TestE2E_UI_Search(t *testing.T) {
	ts, _ := newE2EServer(t)

	resp, body := getE2E(t, ts, "/api/search?q=HandleRequest")

	assertStatus(t, resp, http.StatusOK)
	assertJSONContentType(t, resp)
	assertCORSHeader(t, resp)

	var results []interface{}
	if err := json.Unmarshal(body, &results); err != nil {
		t.Fatalf("decode /api/search: %v", err)
	}
	if len(results) == 0 {
		t.Error("/api/search?q=HandleRequest: expected at least one result")
	}
}

// TestE2E_UI_Search_MissingQuery verifies that GET /api/search without q
// returns 400 with an error field.
func TestE2E_UI_Search_MissingQuery(t *testing.T) {
	ts, _ := newE2EServer(t)

	resp, body := getE2E(t, ts, "/api/search")

	assertStatus(t, resp, http.StatusBadRequest)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode 400 body: %v", err)
	}
	if _, ok := result["error"]; !ok {
		t.Error("/api/search: missing error field in 400 response")
	}
}

// TestE2E_UI_AllEndpoints_CORS verifies that every REST API endpoint includes
// the Access-Control-Allow-Origin: * header.
func TestE2E_UI_AllEndpoints_CORS(t *testing.T) {
	ts, _ := newE2EServer(t)

	endpoints := []string{
		"/api/graph",
		"/api/symbols",
		"/api/symbol/e2e-ui-sym-1",
		"/api/projects",
		"/api/search?q=foo",
	}

	for _, ep := range endpoints {
		resp, err := http.Get(ts.URL + ep)
		if err != nil {
			t.Fatalf("GET %s: %v", ep, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("%s: CORS header = %q, want %q", ep, got, "*")
		}
	}
}

// TestE2E_UI_AllEndpoints_StatusOK verifies that every REST API endpoint
// returns HTTP 200 for valid requests.
func TestE2E_UI_AllEndpoints_StatusOK(t *testing.T) {
	ts, _ := newE2EServer(t)

	endpoints := []struct {
		path string
		want int
	}{
		{"/api/graph", http.StatusOK},
		{"/api/symbols", http.StatusOK},
		{"/api/symbol/e2e-ui-sym-1", http.StatusOK},
		{"/api/projects", http.StatusOK},
		{"/api/search?q=foo", http.StatusOK},
	}

	for _, tc := range endpoints {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("GET %s: status = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
	}
}

// TestE2E_UI_MethodNotAllowed verifies that POST requests to GET-only endpoints
// return 405.
func TestE2E_UI_MethodNotAllowed(t *testing.T) {
	ts, _ := newE2EServer(t)

	endpoints := []string{
		"/api/graph",
		"/api/symbols",
		"/api/projects",
		"/api/search?q=foo",
	}

	for _, ep := range endpoints {
		resp, err := http.Post(ts.URL+ep, "application/json", nil)
		if err != nil {
			t.Fatalf("POST %s: %v", ep, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: status = %d, want 405", ep, resp.StatusCode)
		}
	}
}
