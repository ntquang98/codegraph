//go:build cgo

package cmd

// isCGoBuild returns true when the binary was compiled with CGo enabled.
// This is used in tests to skip tests that require tree-sitter extractors.
func isCGoBuild() bool { return true }
