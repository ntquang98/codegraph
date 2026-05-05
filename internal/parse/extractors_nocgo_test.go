//go:build !cgo

package parse

import (
	"strings"
	"testing"
)

// TestRustExtractor_NoCGo verifies that the no-CGo stub for RustExtractor
// returns an error containing "CGO_ENABLED=1" when Extract is called.
//
// Requirements: 3.4, 3.5
func TestRustExtractor_NoCGo(t *testing.T) {
	e := &RustExtractor{}
	_, _, err := e.Extract("testdata/main.rs", []byte(`fn main() {}`))
	if err == nil {
		t.Fatal("RustExtractor.Extract() expected an error in no-CGo build, got nil")
	}
	if !strings.Contains(err.Error(), "CGO_ENABLED=1") {
		t.Errorf("RustExtractor.Extract() error = %q, want it to contain %q", err.Error(), "CGO_ENABLED=1")
	}
}

// TestZigExtractor_NoCGo verifies that the no-CGo stub for ZigExtractor
// returns an error containing "CGO_ENABLED=1" when Extract is called.
//
// Requirements: 3.4, 3.5
func TestZigExtractor_NoCGo(t *testing.T) {
	e := &ZigExtractor{}
	_, _, err := e.Extract("testdata/main.zig", []byte(`pub fn main() void {}`))
	if err == nil {
		t.Fatal("ZigExtractor.Extract() expected an error in no-CGo build, got nil")
	}
	if !strings.Contains(err.Error(), "CGO_ENABLED=1") {
		t.Errorf("ZigExtractor.Extract() error = %q, want it to contain %q", err.Error(), "CGO_ENABLED=1")
	}
}
