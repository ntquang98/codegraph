package parse

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// Extractor is implemented by each language-specific parser.
// It routes source files to the correct tree-sitter grammar and produces
// Symbols and Edges for the graph store.
type Extractor interface {
	// Language returns the language identifier (e.g. "go", "typescript").
	Language() string

	// Extensions returns the file extensions handled by this extractor
	// (e.g. []string{".go"}).
	Extensions() []string

	// Extract parses src (the full content of the file at path) and returns
	// all symbols and edges found in the file.
	Extract(path string, src []byte) ([]Symbol, []Edge, error)
}

// GenerateSymbolID produces a stable, collision-resistant identifier for a
// symbol. The ID is the hex-encoded first 16 bytes (32 hex characters) of the
// SHA-256 hash of the canonical key:
//
//	projectID + "|" + normalizedFilePath + "|" + symbolName + "|" + kind
//
// normalizedFilePath is the path relative to workspaceRoot with forward
// slashes, ensuring cross-platform consistency.
func GenerateSymbolID(projectID, filePath, symbolName string, kind SymbolKind, workspaceRoot string) string {
	// Normalize the file path to be relative to the workspace root.
	rel, err := filepath.Rel(workspaceRoot, filePath)
	if err != nil {
		// Fall back to the raw path if Rel fails (e.g. different drive on Windows).
		rel = filePath
	}
	// Use forward slashes for cross-platform consistency.
	normalizedPath := strings.ReplaceAll(rel, string(filepath.Separator), "/")

	key := projectID + "|" + normalizedPath + "|" + symbolName + "|" + string(kind)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

// Registry dispatches source files to the correct language Extractor based on
// file extension.
type Registry struct {
	extractors map[string]Extractor // keyed by file extension, e.g. ".go"
}

// NewRegistry returns an empty Registry. Register extractors with Register
// before calling ExtractFile.
func NewRegistry() *Registry {
	return &Registry{
		extractors: make(map[string]Extractor),
	}
}

// Register adds e to the registry for each extension reported by e.Extensions.
// If an extractor is already registered for an extension, it is replaced.
func (r *Registry) Register(e Extractor) {
	for _, ext := range e.Extensions() {
		r.extractors[ext] = e
	}
}

// ExtractFile looks up the extractor for the file extension of path and
// delegates to it. If no extractor is registered for the extension, it returns
// nil, nil, nil (the file is silently skipped, per Requirement 2.5).
func (r *Registry) ExtractFile(path string, src []byte) ([]Symbol, []Edge, error) {
	ext := strings.ToLower(filepath.Ext(path))
	e, ok := r.extractors[ext]
	if !ok {
		return nil, nil, nil
	}
	return e.Extract(path, src)
}

// SupportedExtensions returns all file extensions for which an extractor has
// been registered. The order of the returned slice is not guaranteed.
func (r *Registry) SupportedExtensions() []string {
	exts := make([]string, 0, len(r.extractors))
	for ext := range r.extractors {
		exts = append(exts, ext)
	}
	return exts
}
