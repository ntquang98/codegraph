package parse

// SymbolKind represents the kind of a named code entity.
type SymbolKind string

const (
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindType      SymbolKind = "type"
	KindInterface SymbolKind = "interface"
	KindVariable  SymbolKind = "variable"
	KindModule    SymbolKind = "module"
	KindClass     SymbolKind = "class"
)

// EdgeKind represents the kind of a directed relationship between two symbols.
type EdgeKind string

const (
	EdgeCalls      EdgeKind = "calls"
	EdgeImports    EdgeKind = "imports"
	EdgeImplements EdgeKind = "implements"
	EdgeExtends    EdgeKind = "extends"
	EdgeReferences EdgeKind = "references"
)

// Symbol represents a named code entity extracted from a source file.
type Symbol struct {
	ID         string     // stable hash: hex-encoded first 16 bytes of SHA-256(project+file+name+kind)
	Name       string     // symbol name
	Kind       SymbolKind // function, method, type, interface, variable, module, class
	File       string     // path of the file containing this symbol
	StartLine  int        // 1-based start line
	EndLine    int        // 1-based end line
	Signature  string     // human-readable signature
	DocComment string     // leading doc comment, if any
	ProjectID  string     // owning project identifier
}

// Edge represents a directed relationship between two symbols.
type Edge struct {
	FromID string   // source symbol ID
	ToID   string   // target symbol ID
	Kind   EdgeKind // calls, imports, implements, extends, references
	File   string   // file where the relationship was found
	Line   int      // 1-based line number of the relationship
}
