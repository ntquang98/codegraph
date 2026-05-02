// Package mcp implements an MCP (Model Context Protocol) server that exposes
// the code knowledge graph as JSON-RPC 2.0 tools over HTTP.
//
// The server listens on 127.0.0.1 by default and exposes nine tools:
//
//	codegraph/search        – search symbols by name/kind/project
//	codegraph/callers       – find all callers of a function (recursive, depth-limited)
//	codegraph/callees       – find all functions called by a function
//	codegraph/dependencies  – find what a module/file imports
//	codegraph/dependents    – find what imports a module/file
//	codegraph/implementors  – find all implementations of an interface
//	codegraph/grep          – full-text search across symbol names and signatures
//	codegraph/symbol        – get full details of a symbol by ID
//	codegraph/stats         – get project statistics
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/parse"
)

// ─── JSON-RPC 2.0 wire types ────────────────────────────────────────────────

// Request is a JSON-RPC 2.0 request object.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response object.
type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError is the JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternalError  = -32603
)

// ─── MCP protocol types ──────────────────────────────────────────────────────

// InitializeParams holds the parameters for the MCP initialize request.
type InitializeParams struct {
	ProtocolVersion string      `json:"protocolVersion"`
	ClientInfo      interface{} `json:"clientInfo,omitempty"`
	Capabilities    interface{} `json:"capabilities,omitempty"`
}

// InitializeResult is the response to an MCP initialize request.
type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
	Capabilities    Capabilities `json:"capabilities"`
}

// ServerInfo identifies the MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Capabilities describes what the server supports.
type Capabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

// ToolsCapability signals that the server supports the tools feature.
type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// ToolsListResult is the response to a tools/list request.
type ToolsListResult struct {
	Tools []ToolDefinition `json:"tools"`
}

// ToolDefinition describes a single MCP tool.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// InputSchema is a JSON Schema object describing a tool's input.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// Property is a single JSON Schema property.
type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ToolCallParams holds the parameters for a tools/call request.
type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// ToolCallResult is the response to a tools/call request.
type ToolCallResult struct {
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ContentItem is a single item in a tool call result.
type ContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ─── Server ──────────────────────────────────────────────────────────────────

// Server is the MCP HTTP server.
type Server struct {
	store  *graph.Store
	addr   string
	server *http.Server
}

// NewServer creates a new MCP server bound to addr, backed by store.
func NewServer(store *graph.Store, addr string) *Server {
	s := &Server{
		store: store,
		addr:  addr,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)

	s.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// Start begins listening and serving requests. It blocks until the server is
// stopped or encounters a fatal error.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("mcp: listen %s: %w", s.addr, err)
	}
	if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("mcp: serve: %w", err)
	}
	return nil
}

// Stop gracefully shuts down the server, waiting up to ctx's deadline for
// in-flight requests to complete.
func (s *Server) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// ─── HTTP handler ────────────────────────────────────────────────────────────

// handleMCP is the single HTTP endpoint that handles all JSON-RPC 2.0 requests.
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeResponse(w, Response{
			JSONRPC: "2.0",
			ID:      nil,
			Error:   &RPCError{Code: -32700, Message: "parse error: " + err.Error()},
		})
		return
	}

	var resp Response
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	switch req.Method {
	case "initialize":
		resp.Result = s.handleInitialize(req.Params)
	case "tools/list":
		resp.Result = s.handleToolsList()
	case "tools/call":
		result, rpcErr := s.handleToolsCall(req.Params)
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			resp.Result = result
		}
	default:
		resp.Error = &RPCError{
			Code:    ErrMethodNotFound,
			Message: fmt.Sprintf("method not found: %s", req.Method),
		}
	}

	writeResponse(w, resp)
}

// writeResponse serialises resp as JSON and writes it to w.
func writeResponse(w http.ResponseWriter, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// ─── initialize ──────────────────────────────────────────────────────────────

func (s *Server) handleInitialize(_ json.RawMessage) InitializeResult {
	return InitializeResult{
		ProtocolVersion: "2024-11-05",
		ServerInfo: ServerInfo{
			Name:    "codegraph",
			Version: "1.0.0",
		},
		Capabilities: Capabilities{
			Tools: &ToolsCapability{ListChanged: false},
		},
	}
}

// ─── tools/list ──────────────────────────────────────────────────────────────

func (s *Server) handleToolsList() ToolsListResult {
	return ToolsListResult{Tools: toolDefinitions()}
}

// toolDefinitions returns the canonical list of all nine codegraph tools.
func toolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "codegraph/search",
			Description: "Search for symbols by name, kind, project, or file. Returns matching symbols with their location and metadata.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"name":      {Type: "string", Description: "Symbol name to search for (substring match)"},
					"kind":      {Type: "string", Description: "Symbol kind filter: function, method, type, interface, variable, module, class"},
					"projectId": {Type: "string", Description: "Restrict results to a specific project ID"},
					"file":      {Type: "string", Description: "Restrict results to a specific file path"},
					"limit":     {Type: "number", Description: "Maximum number of results to return (default 20)"},
					"offset":    {Type: "number", Description: "Pagination offset (default 0)"},
				},
			},
		},
		{
			Name:        "codegraph/callers",
			Description: "Find all symbols that (transitively) call the given symbol, up to the specified depth.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"symbolId": {Type: "string", Description: "ID of the target symbol"},
					"depth":    {Type: "number", Description: "Maximum traversal depth (1–10, default 3)"},
				},
				Required: []string{"symbolId"},
			},
		},
		{
			Name:        "codegraph/callees",
			Description: "Find all symbols that the given symbol (transitively) calls, up to the specified depth.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"symbolId": {Type: "string", Description: "ID of the source symbol"},
					"depth":    {Type: "number", Description: "Maximum traversal depth (1–10, default 3)"},
				},
				Required: []string{"symbolId"},
			},
		},
		{
			Name:        "codegraph/dependencies",
			Description: "Find all symbols reachable from the given symbol via import edges, up to the specified depth.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"symbolId": {Type: "string", Description: "ID of the source symbol"},
					"depth":    {Type: "number", Description: "Maximum traversal depth (1–10, default 2)"},
				},
				Required: []string{"symbolId"},
			},
		},
		{
			Name:        "codegraph/dependents",
			Description: "Find all symbols that import the given symbol, up to the specified depth.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"symbolId": {Type: "string", Description: "ID of the target symbol"},
					"depth":    {Type: "number", Description: "Maximum traversal depth (1–10, default 2)"},
				},
				Required: []string{"symbolId"},
			},
		},
		{
			Name:        "codegraph/implementors",
			Description: "Find all symbols that implement the given interface symbol.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"symbolId": {Type: "string", Description: "ID of the interface symbol"},
				},
				Required: []string{"symbolId"},
			},
		},
		{
			Name:        "codegraph/grep",
			Description: "Full-text search across symbol names, signatures, and doc comments.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"query": {Type: "string", Description: "Search query string"},
					"limit": {Type: "number", Description: "Maximum number of results (1–100, default 20)"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "codegraph/symbol",
			Description: "Get full details of a symbol by its ID, including its immediate callers and callees.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"symbolId": {Type: "string", Description: "ID of the symbol to retrieve"},
				},
				Required: []string{"symbolId"},
			},
		},
		{
			Name:        "codegraph/stats",
			Description: "Get aggregate statistics (symbol count, edge count, file count) per project.",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]Property{},
			},
		},
	}
}

// ─── tools/call dispatcher ───────────────────────────────────────────────────

func (s *Server) handleToolsCall(raw json.RawMessage) (*ToolCallResult, *RPCError) {
	var params ToolCallParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "invalid tools/call params: " + err.Error()}
	}

	switch params.Name {
	case "codegraph/search":
		return s.toolSearch(params.Arguments)
	case "codegraph/callers":
		return s.toolCallers(params.Arguments)
	case "codegraph/callees":
		return s.toolCallees(params.Arguments)
	case "codegraph/dependencies":
		return s.toolDependencies(params.Arguments)
	case "codegraph/dependents":
		return s.toolDependents(params.Arguments)
	case "codegraph/implementors":
		return s.toolImplementors(params.Arguments)
	case "codegraph/grep":
		return s.toolGrep(params.Arguments)
	case "codegraph/symbol":
		return s.toolSymbol(params.Arguments)
	case "codegraph/stats":
		return s.toolStats(params.Arguments)
	default:
		return nil, &RPCError{
			Code:    ErrMethodNotFound,
			Message: fmt.Sprintf("unknown tool: %s", params.Name),
		}
	}
}

// ─── tool helpers ────────────────────────────────────────────────────────────

// requireString extracts a required string argument. Returns an error if the
// key is absent or not a string.
func requireString(args map[string]interface{}, key string) (string, *RPCError) {
	v, ok := args[key]
	if !ok {
		return "", &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("missing required argument: %q", key)}
	}
	s, ok := v.(string)
	if !ok {
		return "", &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("argument %q must be a string", key)}
	}
	return s, nil
}

// optionalInt extracts an optional numeric argument, returning defaultVal when
// the key is absent. Returns an error if the key is present but not a number.
func optionalInt(args map[string]interface{}, key string, defaultVal int) (int, *RPCError) {
	v, ok := args[key]
	if !ok {
		return defaultVal, nil
	}
	// JSON numbers unmarshal as float64.
	f, ok := v.(float64)
	if !ok {
		return 0, &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("argument %q must be a number", key)}
	}
	return int(f), nil
}

// optionalString extracts an optional string argument.
func optionalString(args map[string]interface{}, key string) (string, *RPCError) {
	v, ok := args[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", &RPCError{Code: ErrInvalidParams, Message: fmt.Sprintf("argument %q must be a string", key)}
	}
	return s, nil
}

// symbolsToJSON serialises a slice of symbols to a JSON string.
func symbolsToJSON(symbols []parse.Symbol) (string, error) {
	b, err := json.MarshalIndent(symbols, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// textResult wraps a text string in the MCP content envelope.
func textResult(text string) *ToolCallResult {
	return &ToolCallResult{
		Content: []ContentItem{{Type: "text", Text: text}},
	}
}

// ─── individual tool handlers ────────────────────────────────────────────────

func (s *Server) toolSearch(args map[string]interface{}) (*ToolCallResult, *RPCError) {
	name, rpcErr := optionalString(args, "name")
	if rpcErr != nil {
		return nil, rpcErr
	}
	kind, rpcErr := optionalString(args, "kind")
	if rpcErr != nil {
		return nil, rpcErr
	}
	projectID, rpcErr := optionalString(args, "projectId")
	if rpcErr != nil {
		return nil, rpcErr
	}
	file, rpcErr := optionalString(args, "file")
	if rpcErr != nil {
		return nil, rpcErr
	}
	limit, rpcErr := optionalInt(args, "limit", 20)
	if rpcErr != nil {
		return nil, rpcErr
	}
	offset, rpcErr := optionalInt(args, "offset", 0)
	if rpcErr != nil {
		return nil, rpcErr
	}

	symbols, err := s.store.SearchSymbols(graph.SearchQuery{
		Name:      name,
		Kind:      parse.SymbolKind(kind),
		ProjectID: projectID,
		File:      file,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "search failed: " + err.Error()}
	}

	text, err := symbolsToJSON(symbols)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "marshal failed: " + err.Error()}
	}
	return textResult(text), nil
}

func (s *Server) toolCallers(args map[string]interface{}) (*ToolCallResult, *RPCError) {
	symbolID, rpcErr := requireString(args, "symbolId")
	if rpcErr != nil {
		return nil, rpcErr
	}
	depth, rpcErr := optionalInt(args, "depth", 3)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if depth < 1 || depth > 10 {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "depth must be between 1 and 10"}
	}

	symbols, err := s.store.GetCallers(symbolID, depth)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "callers query failed: " + err.Error()}
	}

	text, err := symbolsToJSON(symbols)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "marshal failed: " + err.Error()}
	}
	return textResult(text), nil
}

func (s *Server) toolCallees(args map[string]interface{}) (*ToolCallResult, *RPCError) {
	symbolID, rpcErr := requireString(args, "symbolId")
	if rpcErr != nil {
		return nil, rpcErr
	}
	depth, rpcErr := optionalInt(args, "depth", 3)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if depth < 1 || depth > 10 {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "depth must be between 1 and 10"}
	}

	symbols, err := s.store.GetCallees(symbolID, depth)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "callees query failed: " + err.Error()}
	}

	text, err := symbolsToJSON(symbols)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "marshal failed: " + err.Error()}
	}
	return textResult(text), nil
}

func (s *Server) toolDependencies(args map[string]interface{}) (*ToolCallResult, *RPCError) {
	symbolID, rpcErr := requireString(args, "symbolId")
	if rpcErr != nil {
		return nil, rpcErr
	}
	depth, rpcErr := optionalInt(args, "depth", 2)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if depth < 1 || depth > 10 {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "depth must be between 1 and 10"}
	}

	symbols, err := s.store.GetDependencies(symbolID, depth)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "dependencies query failed: " + err.Error()}
	}

	text, err := symbolsToJSON(symbols)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "marshal failed: " + err.Error()}
	}
	return textResult(text), nil
}

func (s *Server) toolDependents(args map[string]interface{}) (*ToolCallResult, *RPCError) {
	symbolID, rpcErr := requireString(args, "symbolId")
	if rpcErr != nil {
		return nil, rpcErr
	}
	depth, rpcErr := optionalInt(args, "depth", 2)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if depth < 1 || depth > 10 {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "depth must be between 1 and 10"}
	}

	symbols, err := s.store.GetDependents(symbolID, depth)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "dependents query failed: " + err.Error()}
	}

	text, err := symbolsToJSON(symbols)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "marshal failed: " + err.Error()}
	}
	return textResult(text), nil
}

func (s *Server) toolImplementors(args map[string]interface{}) (*ToolCallResult, *RPCError) {
	symbolID, rpcErr := requireString(args, "symbolId")
	if rpcErr != nil {
		return nil, rpcErr
	}

	symbols, err := s.store.GetImplementors(symbolID)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "implementors query failed: " + err.Error()}
	}

	text, err := symbolsToJSON(symbols)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "marshal failed: " + err.Error()}
	}
	return textResult(text), nil
}

func (s *Server) toolGrep(args map[string]interface{}) (*ToolCallResult, *RPCError) {
	query, rpcErr := requireString(args, "query")
	if rpcErr != nil {
		return nil, rpcErr
	}
	limit, rpcErr := optionalInt(args, "limit", 20)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if limit < 1 || limit > 100 {
		return nil, &RPCError{Code: ErrInvalidParams, Message: "limit must be between 1 and 100"}
	}

	symbols, err := s.store.FullTextSearch(query, limit)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "grep failed: " + err.Error()}
	}

	text, err := symbolsToJSON(symbols)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "marshal failed: " + err.Error()}
	}
	return textResult(text), nil
}

// symbolDetail bundles a symbol with its immediate callers and callees.
type symbolDetail struct {
	Symbol  *parse.Symbol  `json:"symbol"`
	Callers []parse.Symbol `json:"callers"`
	Callees []parse.Symbol `json:"callees"`
}

func (s *Server) toolSymbol(args map[string]interface{}) (*ToolCallResult, *RPCError) {
	symbolID, rpcErr := requireString(args, "symbolId")
	if rpcErr != nil {
		return nil, rpcErr
	}

	sym, err := s.store.GetSymbol(symbolID)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "symbol lookup failed: " + err.Error()}
	}
	if sym == nil {
		b, _ := json.Marshal(nil)
		return textResult(string(b)), nil
	}

	callers, err := s.store.GetCallers(symbolID, 1)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "callers lookup failed: " + err.Error()}
	}
	callees, err := s.store.GetCallees(symbolID, 1)
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "callees lookup failed: " + err.Error()}
	}

	detail := symbolDetail{
		Symbol:  sym,
		Callers: callers,
		Callees: callees,
	}
	b, err := json.MarshalIndent(detail, "", "  ")
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "marshal failed: " + err.Error()}
	}
	return textResult(string(b)), nil
}

func (s *Server) toolStats(_ map[string]interface{}) (*ToolCallResult, *RPCError) {
	stats, err := s.store.GetProjectStats()
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "stats query failed: " + err.Error()}
	}

	b, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return nil, &RPCError{Code: ErrInternalError, Message: "marshal failed: " + err.Error()}
	}
	return textResult(string(b)), nil
}
