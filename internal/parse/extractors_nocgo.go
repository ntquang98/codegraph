//go:build !cgo

// Package parse provides language-specific extractors for source code analysis.
// This file provides stub implementations for environments where CGo is not
// available. The full implementations (using go-tree-sitter) are in the
// extractor_*.go files which require CGo.
package parse

import "fmt"

// GoExtractor extracts symbols and edges from Go source files.
// This is a stub implementation for non-CGo builds.
type GoExtractor struct{}

func (e *GoExtractor) Language() string     { return "go" }
func (e *GoExtractor) Extensions() []string { return []string{".go"} }
func (e *GoExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
	return nil, nil, fmt.Errorf("go extractor requires CGo: rebuild with CGO_ENABLED=1 and a C compiler")
}

// TypeScriptExtractor extracts symbols and edges from TypeScript source files.
// This is a stub implementation for non-CGo builds.
type TypeScriptExtractor struct{}

func (e *TypeScriptExtractor) Language() string     { return "typescript" }
func (e *TypeScriptExtractor) Extensions() []string { return []string{".ts", ".tsx"} }
func (e *TypeScriptExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
	return nil, nil, fmt.Errorf("typescript extractor requires CGo: rebuild with CGO_ENABLED=1 and a C compiler")
}

// JavaScriptExtractor extracts symbols and edges from JavaScript source files.
// This is a stub implementation for non-CGo builds.
type JavaScriptExtractor struct{}

func (e *JavaScriptExtractor) Language() string     { return "javascript" }
func (e *JavaScriptExtractor) Extensions() []string { return []string{".js", ".jsx", ".mjs", ".cjs"} }
func (e *JavaScriptExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
	return nil, nil, fmt.Errorf("javascript extractor requires CGo: rebuild with CGO_ENABLED=1 and a C compiler")
}

// PythonExtractor extracts symbols and edges from Python source files.
// This is a stub implementation for non-CGo builds.
type PythonExtractor struct{}

func (e *PythonExtractor) Language() string     { return "python" }
func (e *PythonExtractor) Extensions() []string { return []string{".py"} }
func (e *PythonExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
	return nil, nil, fmt.Errorf("python extractor requires CGo: rebuild with CGO_ENABLED=1 and a C compiler")
}

// CSharpExtractor extracts symbols and edges from C# source files.
// This is a stub implementation for non-CGo builds.
type CSharpExtractor struct{}

func (e *CSharpExtractor) Language() string     { return "csharp" }
func (e *CSharpExtractor) Extensions() []string { return []string{".cs"} }
func (e *CSharpExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
	return nil, nil, fmt.Errorf("csharp extractor requires CGo: rebuild with CGO_ENABLED=1 and a C compiler")
}
