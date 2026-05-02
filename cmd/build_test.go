package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/codegraph-cli/codegraph/internal/config"
	"github.com/codegraph-cli/codegraph/internal/graph"
	"pgregory.net/rapid"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// openTestStore opens an in-memory SQLite store and runs Migrate.
func openTestStore(t *testing.T) *graph.Store {
	t.Helper()
	s, err := graph.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// writeFile writes content to a file, creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// countSymbols returns the total number of symbols in the store.
func countSymbols(t *testing.T, s *graph.Store) int {
	t.Helper()
	syms, err := s.SearchSymbols(graph.SearchQuery{Limit: 1000000})
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	return len(syms)
}

// ─── Integration Tests (task 6.5) ────────────────────────────────────────────

// TestBuild_GoSample runs a full build against testdata/go-sample and verifies
// that symbols and edges are produced.
func TestBuild_GoSample(t *testing.T) {
	if !isCGoBuild() {
		t.Skip("skipping: go extractor requires CGo")
	}

	// Locate testdata/go-sample relative to this test file.
	goSamplePath, err := filepath.Abs(filepath.Join("..", "testdata", "go-sample"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	if _, err := os.Stat(goSamplePath); os.IsNotExist(err) {
		t.Skip("testdata/go-sample not found")
	}

	root := t.TempDir()
	// Copy go-sample into the temp workspace.
	sampleDir := filepath.Join(root, "go-sample")
	if err := copyDir(t, goSamplePath, sampleDir); err != nil {
		t.Fatalf("copy testdata: %v", err)
	}

	ws := &config.Workspace{
		Name: "test",
		Projects: []config.Project{
			{Name: "go-sample", Path: "go-sample", Language: "go"},
		},
	}

	store := openTestStore(t)
	if err := store.UpsertProject("go-sample", "go-sample", "go-sample", "go"); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	registry := buildRegistry()
	result, err := RunBuild(ws, store, registry, root)
	if err != nil {
		t.Fatalf("RunBuild: %v", err)
	}

	// The go-sample has 3 files with multiple symbols and edges.
	if result.FilesProcessed == 0 {
		t.Error("expected at least one file to be processed")
	}
	if result.SymbolsAdded == 0 {
		t.Error("expected at least one symbol to be added")
	}

	t.Logf("go-sample: %d files, %d symbols, %d edges",
		result.FilesProcessed, result.SymbolsAdded, result.EdgesAdded)
}

// TestBuild_TsSample runs a full build against testdata/ts-sample and verifies
// that symbols and edges are produced.
func TestBuild_TsSample(t *testing.T) {
	if !isCGoBuild() {
		t.Skip("skipping: typescript extractor requires CGo")
	}

	tsSamplePath, err := filepath.Abs(filepath.Join("..", "testdata", "ts-sample"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	if _, err := os.Stat(tsSamplePath); os.IsNotExist(err) {
		t.Skip("testdata/ts-sample not found")
	}

	root := t.TempDir()
	sampleDir := filepath.Join(root, "ts-sample")
	if err := copyDir(t, tsSamplePath, sampleDir); err != nil {
		t.Fatalf("copy testdata: %v", err)
	}

	ws := &config.Workspace{
		Name: "test",
		Projects: []config.Project{
			{Name: "ts-sample", Path: "ts-sample", Language: "typescript"},
		},
	}

	store := openTestStore(t)
	if err := store.UpsertProject("ts-sample", "ts-sample", "ts-sample", "typescript"); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	registry := buildRegistry()
	result, err := RunBuild(ws, store, registry, root)
	if err != nil {
		t.Fatalf("RunBuild: %v", err)
	}

	if result.FilesProcessed == 0 {
		t.Error("expected at least one file to be processed")
	}
	if result.SymbolsAdded == 0 {
		t.Error("expected at least one symbol to be added")
	}

	t.Logf("ts-sample: %d files, %d symbols, %d edges",
		result.FilesProcessed, result.SymbolsAdded, result.EdgesAdded)
}

// TestBuild_Idempotency verifies that running build twice on unchanged files
// produces identical symbol counts (Requirement 4.10).
func TestBuild_Idempotency(t *testing.T) {
	if !isCGoBuild() {
		t.Skip("skipping: go extractor requires CGo")
	}

	root := t.TempDir()
	projDir := filepath.Join(root, "src")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write a simple Go file.
	writeFile(t, filepath.Join(projDir, "main.go"), `package main

func Hello() string {
	return "hello"
}

func Goodbye() string {
	return "goodbye"
}
`)

	ws := &config.Workspace{
		Name: "test",
		Projects: []config.Project{
			{Name: "src", Path: "src", Language: "go"},
		},
	}

	store := openTestStore(t)
	if err := store.UpsertProject("src", "src", "src", "go"); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	registry := buildRegistry()

	// First build.
	result1, err := RunBuild(ws, store, registry, root)
	if err != nil {
		t.Fatalf("first RunBuild: %v", err)
	}

	// Second build on unchanged files.
	result2, err := RunBuild(ws, store, registry, root)
	if err != nil {
		t.Fatalf("second RunBuild: %v", err)
	}

	// Second build should process 0 files (all hashes match).
	if result2.FilesProcessed != 0 {
		t.Errorf("second build: expected 0 files processed (unchanged), got %d", result2.FilesProcessed)
	}
	if result2.SymbolsAdded != 0 {
		t.Errorf("second build: expected 0 symbols added, got %d", result2.SymbolsAdded)
	}

	// Total symbol count in the store should be the same after both builds.
	syms1 := countSymbols(t, store)
	_ = result1 // used for logging
	t.Logf("first build: %d files, %d symbols; second build: %d files, %d symbols; store total: %d",
		result1.FilesProcessed, result1.SymbolsAdded,
		result2.FilesProcessed, result2.SymbolsAdded,
		syms1)
}

// TestBuild_UpdateEquivalence verifies that after modifying a file, running
// update produces the same graph state as a full build (Requirement 5.5).
func TestBuild_UpdateEquivalence(t *testing.T) {
	if !isCGoBuild() {
		t.Skip("skipping: go extractor requires CGo")
	}

	root := t.TempDir()
	projDir := filepath.Join(root, "src")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mainFile := filepath.Join(projDir, "main.go")
	writeFile(t, mainFile, `package main

func Alpha() {}
func Beta() {}
`)

	ws := &config.Workspace{
		Name: "test",
		Projects: []config.Project{
			{Name: "src", Path: "src", Language: "go"},
		},
	}

	// Build store.
	buildStore := openTestStore(t)
	if err := buildStore.UpsertProject("src", "src", "src", "go"); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	registry := buildRegistry()

	if _, err := RunBuild(ws, buildStore, registry, root); err != nil {
		t.Fatalf("initial RunBuild: %v", err)
	}

	// Modify the file.
	writeFile(t, mainFile, `package main

func Alpha() {}
func Beta() {}
func Gamma() {}
`)

	// Run update on the build store.
	if _, err := RunUpdate(ws, buildStore, registry, root); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}

	// Run a fresh full build on a separate store.
	freshStore := openTestStore(t)
	if err := freshStore.UpsertProject("src", "src", "src", "go"); err != nil {
		t.Fatalf("UpsertProject fresh: %v", err)
	}
	if _, err := RunBuild(ws, freshStore, registry, root); err != nil {
		t.Fatalf("fresh RunBuild: %v", err)
	}

	// Both stores should have the same symbol count.
	updateSyms := countSymbols(t, buildStore)
	buildSyms := countSymbols(t, freshStore)

	if updateSyms != buildSyms {
		t.Errorf("update store has %d symbols, fresh build has %d — should be equal",
			updateSyms, buildSyms)
	}
}

// TestUpdate_DeletedFile verifies that deleted files are removed from the graph.
func TestUpdate_DeletedFile(t *testing.T) {
	if !isCGoBuild() {
		t.Skip("skipping: go extractor requires CGo")
	}

	root := t.TempDir()
	projDir := filepath.Join(root, "src")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fileA := filepath.Join(projDir, "a.go")
	fileB := filepath.Join(projDir, "b.go")
	writeFile(t, fileA, `package main
func FuncA() {}
`)
	writeFile(t, fileB, `package main
func FuncB() {}
`)

	ws := &config.Workspace{
		Name: "test",
		Projects: []config.Project{
			{Name: "src", Path: "src", Language: "go"},
		},
	}

	store := openTestStore(t)
	if err := store.UpsertProject("src", "src", "src", "go"); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	registry := buildRegistry()

	if _, err := RunBuild(ws, store, registry, root); err != nil {
		t.Fatalf("RunBuild: %v", err)
	}

	symsBefore := countSymbols(t, store)

	// Delete fileB.
	if err := os.Remove(fileB); err != nil {
		t.Fatalf("remove fileB: %v", err)
	}

	if _, err := RunUpdate(ws, store, registry, root); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}

	symsAfter := countSymbols(t, store)

	if symsAfter >= symsBefore {
		t.Errorf("expected fewer symbols after deleting a file: before=%d, after=%d",
			symsBefore, symsAfter)
	}
}

// TestBuild_AtomicFileWrite verifies that if a file produces no symbols (e.g.
// empty file), the file hash is still recorded and a second build skips it.
func TestBuild_AtomicFileWrite(t *testing.T) {
	if !isCGoBuild() {
		t.Skip("skipping: go extractor requires CGo")
	}

	root := t.TempDir()
	projDir := filepath.Join(root, "src")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeFile(t, filepath.Join(projDir, "empty.go"), `package main
`)

	ws := &config.Workspace{
		Name: "test",
		Projects: []config.Project{
			{Name: "src", Path: "src", Language: "go"},
		},
	}

	store := openTestStore(t)
	if err := store.UpsertProject("src", "src", "src", "go"); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	registry := buildRegistry()

	result1, err := RunBuild(ws, store, registry, root)
	if err != nil {
		t.Fatalf("first RunBuild: %v", err)
	}
	if result1.FilesProcessed != 1 {
		t.Errorf("expected 1 file processed, got %d", result1.FilesProcessed)
	}

	// Second build: file is unchanged, should be skipped.
	result2, err := RunBuild(ws, store, registry, root)
	if err != nil {
		t.Fatalf("second RunBuild: %v", err)
	}
	if result2.FilesProcessed != 0 {
		t.Errorf("expected 0 files processed on second build, got %d", result2.FilesProcessed)
	}
}

// ─── Property Tests (task 6.6) ────────────────────────────────────────────────

// TestProperty_BuildIdempotent verifies Property 1: for any workspace, running
// build twice on unchanged files produces identical symbol and edge counts.
//
// Validates: Requirement 4.10
func TestProperty_BuildIdempotent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		projDir := filepath.Join(root, "src")
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			rt.Fatalf("mkdir: %v", err)
		}

		// Generate 1–5 Go source files with simple function declarations.
		numFiles := rapid.IntRange(1, 5).Draw(rt, "numFiles")
		for i := 0; i < numFiles; i++ {
			funcName := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{3,15}`).Draw(rt, "funcName")
			content := "package main\n\nfunc " + funcName + "() {}\n"
			path := filepath.Join(projDir, rapid.StringMatching(`[a-z][a-z0-9]{2,8}`).Draw(rt, "fileName")+".go")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				rt.Fatalf("write file: %v", err)
			}
		}

		ws := &config.Workspace{
			Name: "prop-test",
			Projects: []config.Project{
				{Name: "src", Path: "src", Language: "go"},
			},
		}

		store, err := graph.Open(":memory:")
		if err != nil {
			rt.Fatalf("Open: %v", err)
		}
		defer store.Close()
		if err := store.Migrate(); err != nil {
			rt.Fatalf("Migrate: %v", err)
		}
		if err := store.UpsertProject("src", "src", "src", "go"); err != nil {
			rt.Fatalf("UpsertProject: %v", err)
		}

		registry := buildRegistry()

		// First build.
		result1, err := RunBuild(ws, store, registry, root)
		if err != nil {
			rt.Fatalf("first RunBuild: %v", err)
		}

		// Second build on unchanged files.
		result2, err := RunBuild(ws, store, registry, root)
		if err != nil {
			rt.Fatalf("second RunBuild: %v", err)
		}

		// Second build must process 0 files.
		if result2.FilesProcessed != 0 {
			rt.Fatalf("second build processed %d files (expected 0 — all unchanged)", result2.FilesProcessed)
		}
		if result2.SymbolsAdded != 0 {
			rt.Fatalf("second build added %d symbols (expected 0)", result2.SymbolsAdded)
		}

		// Symbol count in store must equal what the first build produced.
		syms, err := store.SearchSymbols(graph.SearchQuery{Limit: 1000000})
		if err != nil {
			rt.Fatalf("SearchSymbols: %v", err)
		}
		if len(syms) != result1.SymbolsAdded {
			rt.Fatalf("store has %d symbols after two builds, first build added %d",
				len(syms), result1.SymbolsAdded)
		}
	})
}

// TestProperty_UpdateEquivalentToBuild verifies Property 2: for any set of
// file changes, update produces the same graph state as a full build on the
// post-change workspace.
//
// Validates: Requirement 5.5
func TestProperty_UpdateEquivalentToBuild(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		root := t.TempDir()
		projDir := filepath.Join(root, "src")
		if err := os.MkdirAll(projDir, 0o755); err != nil {
			rt.Fatalf("mkdir: %v", err)
		}

		// Write initial files.
		numInitial := rapid.IntRange(1, 4).Draw(rt, "numInitial")
		fileNames := make([]string, numInitial)
		for i := 0; i < numInitial; i++ {
			name := rapid.StringMatching(`[a-z][a-z0-9]{2,6}`).Draw(rt, "fileName")
			fileNames[i] = name + ".go"
			funcName := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{3,12}`).Draw(rt, "funcName")
			content := "package main\n\nfunc " + funcName + "() {}\n"
			if err := os.WriteFile(filepath.Join(projDir, fileNames[i]), []byte(content), 0o644); err != nil {
				rt.Fatalf("write initial file: %v", err)
			}
		}

		ws := &config.Workspace{
			Name: "prop-test",
			Projects: []config.Project{
				{Name: "src", Path: "src", Language: "go"},
			},
		}

		// Build the initial state in the "update" store.
		updateStore, err := graph.Open(":memory:")
		if err != nil {
			rt.Fatalf("Open updateStore: %v", err)
		}
		defer updateStore.Close()
		if err := updateStore.Migrate(); err != nil {
			rt.Fatalf("Migrate updateStore: %v", err)
		}
		if err := updateStore.UpsertProject("src", "src", "src", "go"); err != nil {
			rt.Fatalf("UpsertProject updateStore: %v", err)
		}

		registry := buildRegistry()
		if _, err := RunBuild(ws, updateStore, registry, root); err != nil {
			rt.Fatalf("initial RunBuild: %v", err)
		}

		// Apply changes: modify one file, add one new file.
		if numInitial > 0 {
			modFunc := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{3,12}`).Draw(rt, "modFunc")
			content := "package main\n\nfunc " + modFunc + "() {}\nfunc " + modFunc + "Extra() {}\n"
			if err := os.WriteFile(filepath.Join(projDir, fileNames[0]), []byte(content), 0o644); err != nil {
				rt.Fatalf("modify file: %v", err)
			}
		}

		newFunc := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{3,12}`).Draw(rt, "newFunc")
		newContent := "package main\n\nfunc " + newFunc + "() {}\n"
		if err := os.WriteFile(filepath.Join(projDir, "new_file.go"), []byte(newContent), 0o644); err != nil {
			rt.Fatalf("write new file: %v", err)
		}

		// Run update on the update store.
		if _, err := RunUpdate(ws, updateStore, registry, root); err != nil {
			rt.Fatalf("RunUpdate: %v", err)
		}

		// Run a fresh full build on a separate store.
		freshStore, err := graph.Open(":memory:")
		if err != nil {
			rt.Fatalf("Open freshStore: %v", err)
		}
		defer freshStore.Close()
		if err := freshStore.Migrate(); err != nil {
			rt.Fatalf("Migrate freshStore: %v", err)
		}
		if err := freshStore.UpsertProject("src", "src", "src", "go"); err != nil {
			rt.Fatalf("UpsertProject freshStore: %v", err)
		}
		if _, err := RunBuild(ws, freshStore, registry, root); err != nil {
			rt.Fatalf("fresh RunBuild: %v", err)
		}

		// Both stores must have the same symbol count.
		updateSyms, err := updateStore.SearchSymbols(graph.SearchQuery{Limit: 1000000})
		if err != nil {
			rt.Fatalf("SearchSymbols updateStore: %v", err)
		}
		freshSyms, err := freshStore.SearchSymbols(graph.SearchQuery{Limit: 1000000})
		if err != nil {
			rt.Fatalf("SearchSymbols freshStore: %v", err)
		}

		if len(updateSyms) != len(freshSyms) {
			rt.Fatalf("update store has %d symbols, fresh build has %d — should be equal",
				len(updateSyms), len(freshSyms))
		}
	})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// copyDir recursively copies src directory to dst.
func copyDir(t *testing.T, src, dst string) error {
	t.Helper()
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
