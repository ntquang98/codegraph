package ui_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/parse"
	"github.com/codegraph-cli/codegraph/internal/ui"
)

// ─── Mock store ───────────────────────────────────────────────────────────────

type mockStore struct {
	symbols  []parse.Symbol
	symbol   *parse.Symbol
	callers  []parse.Symbol
	callees  []parse.Symbol
	stats    []graph.ProjectStats
	ftsResult []parse.Symbol
	files    []string
	err      error
}

func (m *mockStore) SearchSymbols(_ graph.SearchQuery) ([]parse.Symbol, error) {
	return m.symbols, m.err
}
func (m *mockStore) GetSymbol(_ string) (*parse.Symbol, error) {
	return m.symbol, m.err
}
func (m *mockStore) GetCallers(_ string, _ int) ([]parse.Symbol, error) {
	return m.callers, m.err
}
func (m *mockStore) GetCallees(_ string, _ int) ([]parse.Symbol, error) {
	return m.callees, m.err
}
func (m *mockStore) GetProjectStats() ([]graph.ProjectStats, error) {
	return m.stats, m.err
}
func (m *mockStore) FullTextSearch(_ string, _ int) ([]parse.Symbol, error) {
	return m.ftsResult, m.err
}
func (m *mockStore) GetAllTrackedFiles() ([]string, error) {
	return m.files, m.err
}

// ─── Test helpers ─────────────────────────────────────────────────────────────

// minimalFS returns a tiny in-memory filesystem with an index.html so the
// file server does not panic.
func minimalFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
}

func newTestServer(store ui.Store) *httptest.Server {
	srv := ui.NewServer(store, "127.0.0.1:0", minimalFS(), true)
	return httptest.NewServer(srv.Handler())
}

func getJSON(t *testing.T, ts *httptest.Server, path string) (*http.Response, map[string]interface{}) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return resp, body
}

func getJSONArray(t *testing.T, ts *httptest.Server, path string) (*http.Response, []interface{}) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	var body []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return resp, body
}

// assertCORS verifies that the CORS header is present.
func assertCORS(t *testing.T, resp *http.Response) {
	t.Helper()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS header: got %q, want %q", got, "*")
	}
}

// assertContentType verifies the Content-Type header.
func assertContentType(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	if got := resp.Header.Get("Content-Type"); got != want {
		t.Errorf("Content-Type: got %q, want %q", got, want)
	}
}

// ─── /api/graph ───────────────────────────────────────────────────────────────

func TestHandleGraph_ReturnsNodesAndEdges(t *testing.T) {
	store := &mockStore{
		symbols: []parse.Symbol{
			{ID: "aaa", Name: "Foo", Kind: parse.KindFunction, File: "a.go", ProjectID: "p1"},
			{ID: "bbb", Name: "Bar", Kind: parse.KindMethod,   File: "a.go", ProjectID: "p1"},
		},
		callees: nil,
	}
	ts := newTestServer(store)
	defer ts.Close()

	resp, body := getJSON(t, ts, "/api/graph")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	assertCORS(t, resp)
	assertContentType(t, resp, "application/json")

	nodes, ok := body["nodes"].([]interface{})
	if !ok {
		t.Fatalf("nodes field missing or wrong type")
	}
	if len(nodes) != 2 {
		t.Errorf("nodes count: got %d, want 2", len(nodes))
	}
	if _, ok := body["edges"]; !ok {
		t.Error("edges field missing")
	}
	if _, ok := body["total_nodes"]; !ok {
		t.Error("total_nodes field missing")
	}
	if _, ok := body["total_edges"]; !ok {
		t.Error("total_edges field missing")
	}
}

func TestHandleGraph_PaginationFields(t *testing.T) {
	store := &mockStore{symbols: []parse.Symbol{}}
	ts := newTestServer(store)
	defer ts.Close()

	resp, body := getJSON(t, ts, "/api/graph?page=2&page_size=10")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if page, ok := body["page"].(float64); !ok || int(page) != 2 {
		t.Errorf("page: got %v, want 2", body["page"])
	}
	if ps, ok := body["page_size"].(float64); !ok || int(ps) != 10 {
		t.Errorf("page_size: got %v, want 10", body["page_size"])
	}
}

func TestHandleGraph_MethodNotAllowed(t *testing.T) {
	ts := newTestServer(&mockStore{})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/graph", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
}

func TestHandleGraph_CORSPreflight(t *testing.T) {
	ts := newTestServer(&mockStore{})
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/api/graph", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status: got %d, want 204", resp.StatusCode)
	}
	assertCORS(t, resp)
}

// ─── /api/symbols ─────────────────────────────────────────────────────────────

func TestHandleSymbols_ReturnsArray(t *testing.T) {
	store := &mockStore{
		symbols: []parse.Symbol{
			{ID: "x1", Name: "Login", Kind: parse.KindFunction, File: "auth.go", ProjectID: "p1"},
		},
	}
	ts := newTestServer(store)
	defer ts.Close()

	resp, arr := getJSONArray(t, ts, "/api/symbols?name=Login")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	assertCORS(t, resp)
	if len(arr) != 1 {
		t.Errorf("symbols count: got %d, want 1", len(arr))
	}
}

func TestHandleSymbols_EmptyResult(t *testing.T) {
	ts := newTestServer(&mockStore{symbols: []parse.Symbol{}})
	defer ts.Close()

	resp, arr := getJSONArray(t, ts, "/api/symbols")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if arr == nil {
		arr = []interface{}{}
	}
	if len(arr) != 0 {
		t.Errorf("expected empty array, got %d items", len(arr))
	}
}

func TestHandleSymbols_CORSHeader(t *testing.T) {
	ts := newTestServer(&mockStore{symbols: []parse.Symbol{}})
	defer ts.Close()

	resp, _ := getJSONArray(t, ts, "/api/symbols")
	assertCORS(t, resp)
}

// ─── /api/symbol/:id ──────────────────────────────────────────────────────────

func TestHandleSymbol_Found(t *testing.T) {
	sym := &parse.Symbol{ID: "abc123", Name: "Foo", Kind: parse.KindFunction, File: "foo.go", ProjectID: "p1"}
	store := &mockStore{
		symbol:  sym,
		callers: []parse.Symbol{{ID: "caller1", Name: "Main", Kind: parse.KindFunction}},
		callees: []parse.Symbol{{ID: "callee1", Name: "Bar",  Kind: parse.KindFunction}},
	}
	ts := newTestServer(store)
	defer ts.Close()

	resp, body := getJSON(t, ts, "/api/symbol/abc123")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	assertCORS(t, resp)

	if _, ok := body["symbol"]; !ok {
		t.Error("symbol field missing")
	}
	if _, ok := body["callers"]; !ok {
		t.Error("callers field missing")
	}
	if _, ok := body["callees"]; !ok {
		t.Error("callees field missing")
	}
}

func TestHandleSymbol_NotFound(t *testing.T) {
	ts := newTestServer(&mockStore{symbol: nil})
	defer ts.Close()

	resp, body := getJSON(t, ts, "/api/symbol/nonexistent")

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", resp.StatusCode)
	}
	if _, ok := body["error"]; !ok {
		t.Error("error field missing in 404 response")
	}
}

func TestHandleSymbol_MissingID(t *testing.T) {
	ts := newTestServer(&mockStore{})
	defer ts.Close()

	resp, body := getJSON(t, ts, "/api/symbol/")

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
	if _, ok := body["error"]; !ok {
		t.Error("error field missing in 400 response")
	}
}

func TestHandleSymbol_CORSHeader(t *testing.T) {
	sym := &parse.Symbol{ID: "z1", Name: "Z", Kind: parse.KindFunction, File: "z.go", ProjectID: "p"}
	ts := newTestServer(&mockStore{symbol: sym})
	defer ts.Close()

	resp, _ := getJSON(t, ts, "/api/symbol/z1")
	assertCORS(t, resp)
}

// ─── /api/projects ────────────────────────────────────────────────────────────

func TestHandleProjects_ReturnsStats(t *testing.T) {
	store := &mockStore{
		stats: []graph.ProjectStats{
			{ProjectID: "proj1", SymbolCount: 10, EdgeCount: 5, FileCount: 3},
		},
	}
	ts := newTestServer(store)
	defer ts.Close()

	resp, arr := getJSONArray(t, ts, "/api/projects")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	assertCORS(t, resp)
	if len(arr) != 1 {
		t.Errorf("projects count: got %d, want 1", len(arr))
	}
}

func TestHandleProjects_EmptyStats(t *testing.T) {
	ts := newTestServer(&mockStore{stats: []graph.ProjectStats{}})
	defer ts.Close()

	resp, _ := getJSONArray(t, ts, "/api/projects")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
}

func TestHandleProjects_MethodNotAllowed(t *testing.T) {
	ts := newTestServer(&mockStore{})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/projects", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
}

// ─── /api/search ──────────────────────────────────────────────────────────────

func TestHandleSearch_ReturnsResults(t *testing.T) {
	store := &mockStore{
		ftsResult: []parse.Symbol{
			{ID: "s1", Name: "handleAuth", Kind: parse.KindFunction, File: "auth.go", ProjectID: "p1"},
		},
	}
	ts := newTestServer(store)
	defer ts.Close()

	resp, arr := getJSONArray(t, ts, "/api/search?q=handleAuth")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	assertCORS(t, resp)
	if len(arr) != 1 {
		t.Errorf("results count: got %d, want 1", len(arr))
	}
}

func TestHandleSearch_MissingQuery(t *testing.T) {
	ts := newTestServer(&mockStore{})
	defer ts.Close()

	resp, body := getJSON(t, ts, "/api/search")

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", resp.StatusCode)
	}
	if _, ok := body["error"]; !ok {
		t.Error("error field missing in 400 response")
	}
}

func TestHandleSearch_CORSHeader(t *testing.T) {
	ts := newTestServer(&mockStore{ftsResult: []parse.Symbol{}})
	defer ts.Close()

	resp, _ := getJSONArray(t, ts, "/api/search?q=foo")
	assertCORS(t, resp)
}

func TestHandleSearch_MethodNotAllowed(t *testing.T) {
	ts := newTestServer(&mockStore{})
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/search?q=foo", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
}

// ─── CORS on all endpoints ────────────────────────────────────────────────────

func TestAllEndpoints_HaveCORSHeaders(t *testing.T) {
	sym := &parse.Symbol{ID: "id1", Name: "Fn", Kind: parse.KindFunction, File: "f.go", ProjectID: "p"}
	store := &mockStore{
		symbols:   []parse.Symbol{*sym},
		symbol:    sym,
		stats:     []graph.ProjectStats{{ProjectID: "p"}},
		ftsResult: []parse.Symbol{*sym},
	}
	ts := newTestServer(store)
	defer ts.Close()

	endpoints := []string{
		"/api/graph",
		"/api/symbols",
		"/api/symbol/id1",
		"/api/projects",
		"/api/search?q=Fn",
	}

	for _, ep := range endpoints {
		resp, err := http.Get(ts.URL + ep)
		if err != nil {
			t.Fatalf("GET %s: %v", ep, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("%s: CORS header missing or wrong: %q", ep, got)
		}
	}
}
