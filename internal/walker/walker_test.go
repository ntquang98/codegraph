package walker

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// ---- helpers ----------------------------------------------------------------

// makeTree creates a directory tree under root.
// Each entry in files is a relative path; directories are created automatically.
func makeTree(t *testing.T, root string, files []string) {
	t.Helper()
	for _, f := range files {
		abs := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("makeTree: mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(""), 0o644); err != nil {
			t.Fatalf("makeTree: write %s: %v", abs, err)
		}
	}
}

// relPaths converts a slice of absolute paths to slash-separated paths
// relative to root, for easier assertion.
func relPaths(t *testing.T, root string, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("relPaths: %v", err)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

// ---- unit tests -------------------------------------------------------------

// TestWalk_BasicWalk verifies that Walk returns all files with supported
// extensions when no exclude patterns are given.
func TestWalk_BasicWalk(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{
		"main.go",
		"util.go",
		"README.md",
		"sub/helper.go",
	})

	got, err := Walk(root, nil, []string{".go"})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	want := []string{"main.go", "sub/helper.go", "util.go"}
	if got := relPaths(t, root, got); !equalSlices(got, want) {
		t.Errorf("Walk() = %v, want %v", got, want)
	}
}

// TestWalk_ExcludeByGlob verifies that files matching a glob pattern are excluded.
func TestWalk_ExcludeByGlob(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{
		"main.go",
		"main_test.go",
		"util_test.go",
		"helper.go",
	})

	got, err := Walk(root, []string{"*_test.go"}, []string{".go"})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	want := []string{"helper.go", "main.go"}
	if got := relPaths(t, root, got); !equalSlices(got, want) {
		t.Errorf("Walk() = %v, want %v", got, want)
	}
}

// TestWalk_ExcludeDirectory verifies that an excluded directory causes the
// entire subtree to be skipped.
func TestWalk_ExcludeDirectory(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{
		"main.go",
		"vendor/dep.go",
		"vendor/sub/deep.go",
		"src/app.go",
	})

	got, err := Walk(root, []string{"vendor"}, []string{".go"})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	want := []string{"main.go", "src/app.go"}
	if got := relPaths(t, root, got); !equalSlices(got, want) {
		t.Errorf("Walk() = %v, want %v", got, want)
	}
}

// TestWalk_ExcludeDirectoryWithSlash verifies that a pattern ending in '/'
// matches directories only and skips the subtree.
func TestWalk_ExcludeDirectoryWithSlash(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{
		"node_modules/lib.js",
		"node_modules/sub/deep.js",
		"src/index.js",
	})

	got, err := Walk(root, []string{"node_modules/"}, []string{".js"})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	want := []string{"src/index.js"}
	if got := relPaths(t, root, got); !equalSlices(got, want) {
		t.Errorf("Walk() = %v, want %v", got, want)
	}
}

// TestWalk_EmptyDirectory verifies that Walk returns an empty slice for an
// empty directory without error.
func TestWalk_EmptyDirectory(t *testing.T) {
	root := t.TempDir()

	got, err := Walk(root, nil, []string{".go"})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Walk() = %v, want empty slice", got)
	}
}

// TestWalk_UnsupportedExtensionFiltering verifies that files with extensions
// not in supportedExts are excluded from results.
func TestWalk_UnsupportedExtensionFiltering(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{
		"main.go",
		"style.css",
		"index.html",
		"app.ts",
	})

	got, err := Walk(root, nil, []string{".go", ".ts"})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	want := []string{"app.ts", "main.go"}
	if got := relPaths(t, root, got); !equalSlices(got, want) {
		t.Errorf("Walk() = %v, want %v", got, want)
	}
}

// TestWalk_DoubleStarPattern verifies that ** matches across directory
// boundaries.
func TestWalk_DoubleStarPattern(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{
		"a/b/c/deep.go",
		"a/b/c/keep.ts",
		"top.go",
	})

	// Exclude all .go files anywhere under a/
	got, err := Walk(root, []string{"a/**/*.go"}, []string{".go", ".ts"})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	want := []string{"a/b/c/keep.ts", "top.go"}
	if got := relPaths(t, root, got); !equalSlices(got, want) {
		t.Errorf("Walk() = %v, want %v", got, want)
	}
}

// TestWalk_PathPatternWithSeparator verifies that a pattern containing a
// separator is matched against the relative path, not just the basename.
func TestWalk_PathPatternWithSeparator(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{
		"dist/bundle.js",
		"src/bundle.js",
	})

	// Only exclude dist/bundle.js, not src/bundle.js
	got, err := Walk(root, []string{"dist/bundle.js"}, []string{".js"})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}

	want := []string{"src/bundle.js"}
	if got := relPaths(t, root, got); !equalSlices(got, want) {
		t.Errorf("Walk() = %v, want %v", got, want)
	}
}

// TestWalk_ReturnsAbsolutePaths verifies that all returned paths are absolute.
func TestWalk_ReturnsAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	makeTree(t, root, []string{"main.go"})

	got, err := Walk(root, nil, []string{".go"})
	if err != nil {
		t.Fatalf("Walk() error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if !filepath.IsAbs(got[0]) {
		t.Errorf("Walk() returned non-absolute path: %q", got[0])
	}
}

// ---- property test (task 3.4) -----------------------------------------------

// TestProperty_NoExcludedFilesReturned verifies that for any set of exclude
// patterns, no file returned by Walk matches any of the exclude patterns.
//
// **Validates: Requirements 4.5**
func TestProperty_NoExcludedFilesReturned(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()

		// Generate a small set of files to create in the tree.
		numFiles := rapid.IntRange(0, 10).Draw(rt, "numFiles")
		fileNames := make([]string, 0, numFiles)
		for i := 0; i < numFiles; i++ {
			// Generate a simple relative path like "dir/file.ext"
			dir := rapid.SampledFrom([]string{"", "sub", "a/b", "vendor", "node_modules"}).Draw(rt, "dir")
			base := rapid.StringMatching(`[a-z][a-z0-9]{0,8}`).Draw(rt, "base")
			ext := rapid.SampledFrom([]string{".go", ".ts", ".js", ".py", ".txt"}).Draw(rt, "ext")

			var rel string
			if dir == "" {
				rel = base + ext
			} else {
				rel = dir + "/" + base + ext
			}
			fileNames = append(fileNames, rel)
		}

		makeTree(t, root, fileNames)

		// Generate a small set of exclude patterns.
		numPatterns := rapid.IntRange(0, 5).Draw(rt, "numPatterns")
		patterns := make([]string, 0, numPatterns)
		for i := 0; i < numPatterns; i++ {
			p := rapid.SampledFrom([]string{
				"vendor",
				"node_modules",
				"*.txt",
				"sub/*.go",
				"**/*.py",
				"a/",
				"a/b/",
			}).Draw(rt, "pattern")
			patterns = append(patterns, p)
		}

		supportedExts := []string{".go", ".ts", ".js", ".py", ".txt"}

		results, err := Walk(root, patterns, supportedExts)
		if err != nil {
			rt.Fatalf("Walk() error: %v", err)
		}

		// For each returned file, verify it does not match any exclude pattern.
		for _, absPath := range results {
			rel, err := filepath.Rel(root, absPath)
			if err != nil {
				rt.Fatalf("filepath.Rel error: %v", err)
			}
			relSlash := filepath.ToSlash(rel)
			name := filepath.Base(rel)
			isDir := false // Walk only returns files

			for _, pattern := range patterns {
				if matchesExclude(pattern, relSlash, name, isDir) {
					rt.Fatalf(
						"Walk() returned file %q that matches exclude pattern %q",
						relSlash, pattern,
					)
				}
			}

			// Also verify the file's parent directories don't match a dir-only pattern.
			parts := strings.Split(relSlash, "/")
			for depth := 1; depth < len(parts); depth++ {
				dirRel := strings.Join(parts[:depth], "/")
				dirName := parts[depth-1]
				for _, pattern := range patterns {
					if matchesExclude(pattern, dirRel, dirName, true) {
						rt.Fatalf(
							"Walk() returned file %q whose ancestor dir %q matches exclude pattern %q",
							relSlash, dirRel, pattern,
						)
					}
				}
			}
		}
	})
}

// ---- helpers ----------------------------------------------------------------

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
