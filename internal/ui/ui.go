// Package ui implements the browser-based graph visualization HTTP server.
//
// The server embeds the frontend assets (HTML/JS/CSS) and exposes a REST API
// for the frontend to query the code graph:
//
//	GET /api/graph          – paginated nodes + edges
//	GET /api/symbols        – symbol search
//	GET /api/symbol/:id     – symbol detail + immediate neighbours
//	GET /api/projects       – project statistics
//	GET /api/search?q=...   – full-text search
//
// All API responses include CORS headers so the frontend can be served from
// any origin during local development.
//
// The server binds to 127.0.0.1 by default (Requirement 8.10). A warning is
// printed when started with a non-localhost --host value.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/parse"
)

// ─── Store interface ──────────────────────────────────────────────────────────

// Store is the subset of graph.Store methods used by the UI server.
// Defining it as an interface makes the server easy to test with a mock.
type Store interface {
	SearchSymbols(query graph.SearchQuery) ([]parse.Symbol, error)
	CountSymbols(query graph.SearchQuery) (int, error)
	GetSymbol(id string) (*parse.Symbol, error)
	GetCallers(symbolID string, depth int) ([]parse.Symbol, error)
	GetCallees(symbolID string, depth int) ([]parse.Symbol, error)
	GetProjectStats() ([]graph.ProjectStats, error)
	FullTextSearch(query string, limit int) ([]parse.Symbol, error)
	GetAllTrackedFiles() ([]string, error)
	GetAllEdges() ([]parse.Edge, error)
	GetEdgesForNodes(nodeIDs map[string]struct{}) ([]parse.Edge, error)
	GetEdgesFromNodes(nodeIDs map[string]struct{}) ([]parse.Edge, error)
}

// ─── Server ───────────────────────────────────────────────────────────────────

// Server is the UI HTTP server.
type Server struct {
	store  Store
	addr   string
	assets fs.FS
	server *http.Server
	noOpen bool
}

// NewServer creates a new UI server bound to addr, backed by store, serving
// static assets from assets. Set noOpen to true to suppress browser auto-open.
func NewServer(store Store, addr string, assets fs.FS, noOpen bool) *Server {
	s := &Server{
		store:  store,
		addr:   addr,
		assets: assets,
		noOpen: noOpen,
	}

	mux := http.NewServeMux()

	// REST API routes
	mux.HandleFunc("/api/graph",    s.handleGraph)
	mux.HandleFunc("/api/symbols",  s.handleSymbols)
	mux.HandleFunc("/api/symbol/",  s.handleSymbol)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/search",   s.handleSearch)

	// Static frontend assets (catch-all)
	mux.Handle("/", http.FileServer(http.FS(assets)))

	s.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// Handler returns the underlying http.Handler so it can be used with
// httptest.NewServer in tests.
func (s *Server) Handler() http.Handler {
	return s.server.Handler
}

// Start begins listening and serving requests. It blocks until the server is
// stopped or encounters a fatal error.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("ui: listen %s: %w", s.addr, err)
	}

	// Auto-open browser unless suppressed (Requirement 8.2).
	if !s.noOpen {
		url := fmt.Sprintf("http://%s", s.addr)
		go openBrowser(url)
	}

	if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("ui: serve: %w", err)
	}
	return nil
}

// Stop gracefully shuts down the server, waiting up to ctx's deadline for
// in-flight requests to complete.
func (s *Server) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// ─── CORS middleware ──────────────────────────────────────────────────────────

// corsHeaders adds CORS headers to every API response (Requirement 8.9).
func corsHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// writeJSON serialises v as JSON and writes it to w with CORS headers.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	corsHeaders(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ─── /api/graph ───────────────────────────────────────────────────────────────

// graphResponse is the paginated response for GET /api/graph.
type graphResponse struct {
	Nodes      []parse.Symbol `json:"nodes"`
	Edges      []edgeDTO      `json:"edges"`
	TotalNodes int            `json:"total_nodes"`
	TotalEdges int            `json:"total_edges"`
	Page       int            `json:"page"`
	PageSize   int            `json:"page_size"`
}

// edgeDTO is a lightweight edge representation for the frontend.
type edgeDTO struct {
	FromID string `json:"FromID"`
	ToID   string `json:"ToID"`
	Kind   string `json:"Kind"`
}

// handleGraph returns a paginated list of symbols (nodes) and their outgoing
// edges. Module-kind symbols are excluded by default since they represent
// files rather than code entities and flood the graph.
//
// Query params:
//
//	page        (default 1)
//	page_size   (default 100, max 500)
//	project_id  (optional, filter by project)
//	kind        (optional, filter by symbol kind; use "module" to include modules)
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	page     := queryInt(r, "page",      1)
	pageSize := queryInt(r, "page_size", 100)
	if page < 1       { page = 1 }
	if pageSize < 1   { pageSize = 1 }
	if pageSize > 500 { pageSize = 500 }

	offset    := (page - 1) * pageSize
	kindParam := parse.SymbolKind(r.URL.Query().Get("kind"))
	projectID := r.URL.Query().Get("project_id")

	// Exclude module symbols by default — they represent files, not code
	// entities, and dominate the graph when included.
	excludeModules := kindParam == "" || kindParam == "module"
	if kindParam == "module" {
		// Explicit request for modules — show them.
		excludeModules = false
	}

	q := graph.SearchQuery{
		Kind:      kindParam,
		ProjectID: projectID,
		Limit:     pageSize,
		Offset:    offset,
	}
	// When no kind filter is set, exclude modules server-side.
	if excludeModules && kindParam == "" {
		q.ExcludeKind = parse.KindModule
	}

	symbols, err := s.store.SearchSymbols(q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "query failed: "+err.Error())
		return
	}

	// Build a set of node IDs on this page.
	nodeIDs := make(map[string]struct{}, len(symbols))
	for _, sym := range symbols {
		nodeIDs[sym.ID] = struct{}{}
	}

	// Fetch edges where the from_id is in the current page.
	// This shows outgoing connections even when the target isn't on this page.
	edges, err := s.store.GetEdgesFromNodes(nodeIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "edge query failed: "+err.Error())
		return
	}

	// Convert to DTO, deduplicating by (from, to, kind).
	edgeSet := make(map[string]edgeDTO, len(edges))
	for _, e := range edges {
		key := e.FromID + "→" + e.ToID + ":" + string(e.Kind)
		edgeSet[key] = edgeDTO{FromID: e.FromID, ToID: e.ToID, Kind: string(e.Kind)}
	}
	edgeDTOs := make([]edgeDTO, 0, len(edgeSet))
	for _, e := range edgeSet {
		edgeDTOs = append(edgeDTOs, e)
	}

	// Total symbol count (excluding modules for consistency with the page).
	totalNodes, err := s.store.CountSymbols(graph.SearchQuery{
		ExcludeKind: parse.KindModule,
		ProjectID:   projectID,
	})
	if err != nil {
		totalNodes = len(symbols) // fallback
	}

	writeJSON(w, http.StatusOK, graphResponse{
		Nodes:      symbols,
		Edges:      edgeDTOs,
		TotalNodes: totalNodes,
		TotalEdges: len(edgeDTOs),
		Page:       page,
		PageSize:   pageSize,
	})
}

// ─── /api/symbols ─────────────────────────────────────────────────────────────

// handleSymbols returns symbols matching search parameters.
// Query params: name, kind, project_id, file, limit (default 50), offset (default 0).
func (s *Server) handleSymbols(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query()
	limit  := queryInt(r, "limit",  50)
	offset := queryInt(r, "offset", 0)
	if limit < 1  { limit = 1 }
	if limit > 500 { limit = 500 }

	symbols, err := s.store.SearchSymbols(graph.SearchQuery{
		Name:      q.Get("name"),
		Kind:      parse.SymbolKind(q.Get("kind")),
		ProjectID: q.Get("project_id"),
		File:      q.Get("file"),
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, symbols)
}

// ─── /api/symbol/:id ──────────────────────────────────────────────────────────

// symbolDetailResponse bundles a symbol with its immediate callers and callees.
type symbolDetailResponse struct {
	Symbol  *parse.Symbol  `json:"symbol"`
	Callers []parse.Symbol `json:"callers"`
	Callees []parse.Symbol `json:"callees"`
}

// handleSymbol returns the full details of a symbol and its immediate neighbours.
// Path: /api/symbol/{id}
func (s *Server) handleSymbol(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract ID from path: /api/symbol/{id}
	id := strings.TrimPrefix(r.URL.Path, "/api/symbol/")
	id = strings.TrimSpace(id)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing symbol id")
		return
	}

	sym, err := s.store.GetSymbol(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "lookup failed: "+err.Error())
		return
	}
	if sym == nil {
		writeError(w, http.StatusNotFound, "symbol not found")
		return
	}

	callers, err := s.store.GetCallers(id, 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "callers failed: "+err.Error())
		return
	}
	callees, err := s.store.GetCallees(id, 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "callees failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, symbolDetailResponse{
		Symbol:  sym,
		Callers: callers,
		Callees: callees,
	})
}

// ─── /api/projects ────────────────────────────────────────────────────────────

// handleProjects returns project statistics.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	stats, err := s.store.GetProjectStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// ─── /api/search ──────────────────────────────────────────────────────────────

// handleSearch performs a full-text search over symbol names and signatures.
// Query params: q (required), limit (default 20, max 100).
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		corsHeaders(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "missing query parameter 'q'")
		return
	}

	limit := queryInt(r, "limit", 20)
	if limit < 1   { limit = 1 }
	if limit > 100 { limit = 100 }

	symbols, err := s.store.FullTextSearch(q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, symbols)
}

// ─── Browser auto-open ────────────────────────────────────────────────────────

// openBrowser opens url in the default system browser (Requirement 8.2).
// Errors are silently ignored — the server continues running regardless.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux and others
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// queryInt parses an integer query parameter, returning defaultVal on error or
// when the parameter is absent.
func queryInt(r *http.Request, key string, defaultVal int) int {
	s := r.URL.Query().Get(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}
