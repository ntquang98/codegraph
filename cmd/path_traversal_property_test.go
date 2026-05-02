package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// TestProperty_PathTraversalRejected verifies Property 17:
// For any file path string (including path traversal attempts), the path
// validation function rejects paths that resolve outside the workspace root.
//
// Validates: Requirements 11.2, 11.3
func TestProperty_PathTraversalRejected(t *testing.T) {
	// Create a real workspace root on disk so EvalSymlinks works.
	wsRoot, err := os.MkdirTemp("", "codegraph-ws-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(wsRoot)

	rapid.Check(t, func(rt *rapid.T) {
		// Generate a path component that may contain traversal sequences.
		// We use a generator that can produce normal segments, "..", ".", and
		// combinations thereof.
		segment := rapid.OneOf(
			rapid.StringMatching(`[a-zA-Z0-9_-]{1,10}`),
			rapid.Just(".."),
			rapid.Just("."),
			rapid.Just("etc"),
			rapid.Just("passwd"),
			rapid.Just("tmp"),
		)

		// Build a path from 1–6 segments.
		numSegments := rapid.IntRange(1, 6).Draw(rt, "numSegments")
		parts := make([]string, numSegments)
		for i := 0; i < numSegments; i++ {
			parts[i] = segment.Draw(rt, "seg")
		}

		// Construct the candidate path by joining segments under wsRoot.
		// This simulates a user-supplied relative path being resolved.
		relPath := filepath.Join(parts...)
		absPath := filepath.Join(wsRoot, relPath)

		// Normalize to determine the ground truth: does this path escape wsRoot?
		cleanedAbs := filepath.Clean(absPath)
		cleanedRoot := filepath.Clean(wsRoot)

		rootWithSep := cleanedRoot
		if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
			rootWithSep += string(filepath.Separator)
		}

		escapesRoot := cleanedAbs != cleanedRoot &&
			!strings.HasPrefix(cleanedAbs, rootWithSep)

		err := ValidatePathWithinWorkspace(wsRoot, absPath)

		if escapesRoot {
			// The path escapes the workspace root — validation MUST return an error.
			if err == nil {
				rt.Fatalf(
					"ValidatePathWithinWorkspace accepted path %q that escapes workspace root %q (cleaned: %q)",
					absPath, wsRoot, cleanedAbs,
				)
			}
		} else {
			// The path is within the workspace root — validation MUST return nil.
			if err != nil {
				rt.Fatalf(
					"ValidatePathWithinWorkspace rejected safe path %q within workspace root %q: %v",
					absPath, wsRoot, err,
				)
			}
		}
	})
}

// TestProperty_ExplicitTraversalAttempts verifies that well-known path
// traversal patterns are always rejected.
//
// Validates: Requirements 11.2, 11.3
func TestProperty_ExplicitTraversalAttempts(t *testing.T) {
	wsRoot, err := os.MkdirTemp("", "codegraph-ws-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(wsRoot)

	rapid.Check(t, func(rt *rapid.T) {
		// Generate a "safe" sub-path within the workspace.
		safeSub := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_/]{0,20}`).Draw(rt, "safeSub")

		// Generate a number of ".." hops (1–5) to escape the workspace.
		hops := rapid.IntRange(1, 5).Draw(rt, "hops")

		// Build a traversal path: wsRoot/safeSub/../../.. (hops times)
		parts := []string{wsRoot, safeSub}
		for i := 0; i < hops; i++ {
			parts = append(parts, "..")
		}
		// Append a target outside the workspace.
		parts = append(parts, "etc", "passwd")
		traversalPath := filepath.Join(parts...)

		// This path, when cleaned, will escape wsRoot (since we have more ".."
		// hops than the depth of safeSub within wsRoot).
		cleanedTraversal := filepath.Clean(traversalPath)
		cleanedRoot := filepath.Clean(wsRoot)

		rootWithSep := cleanedRoot
		if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
			rootWithSep += string(filepath.Separator)
		}

		escapesRoot := cleanedTraversal != cleanedRoot &&
			!strings.HasPrefix(cleanedTraversal, rootWithSep)

		if !escapesRoot {
			// The generated path happened to stay within the workspace (e.g.,
			// safeSub had enough depth). Skip this case — it's not a traversal.
			rt.Skip()
		}

		err := ValidatePathWithinWorkspace(wsRoot, traversalPath)
		if err == nil {
			rt.Fatalf(
				"ValidatePathWithinWorkspace accepted traversal path %q (cleaned: %q) that escapes workspace root %q",
				traversalPath, cleanedTraversal, wsRoot,
			)
		}
	})
}
