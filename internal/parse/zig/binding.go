//go:build cgo

// Package zig provides Go bindings for the tree-sitter Zig grammar.
// The parser.c is sourced from github.com/maxxnino/tree-sitter-zig.
package zig

// #cgo CFLAGS: -I${SRCDIR}
// #include "parser.h"
// TSLanguage *tree_sitter_zig(void);
import "C"
import (
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

// GetLanguage returns the tree-sitter Language for Zig.
func GetLanguage() *sitter.Language {
	ptr := unsafe.Pointer(C.tree_sitter_zig())
	return sitter.NewLanguage(ptr)
}
