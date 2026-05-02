//go:build !cgo

package cmd

// isCGoBuild returns false when the binary was compiled without CGo.
func isCGoBuild() bool { return false }
