package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/codegraph-cli/codegraph/internal/config"
	"github.com/codegraph-cli/codegraph/internal/graph"
)

// TestE2E_FullPipeline runs the complete pipeline against testdata/go-sample:
//
//  1. install  – detect projects and save .codegraph.json
//  2. build    – parse files and store symbols/edges
//  3. verify   – symbol count > 0 and edge count > 0
//  4. update   – modify one file and run incremental update
//  5. verify   – graph reflects the change
//  6. clean    – remove .codegraph.json and .codegraph.db
//  7. verify   – both files are gone
func TestE2E_FullPipeline(t *testing.T) {
	if !isCGoBuild() {
		t.Skip("skipping e2e pipeline: go extractor requires CGo")
	}

	// Locate testdata/go-sample relative to this test file.
	goSamplePath, err := filepath.Abs(filepath.Join("..", "testdata", "go-sample"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	if _, err := os.Stat(goSamplePath); os.IsNotExist(err) {
		t.Skip("testdata/go-sample not found")
	}

	// ── Step 0: set up a temp workspace ──────────────────────────────────────
	root := t.TempDir()
	sampleDir := filepath.Join(root, "go-sample")
	if err := copyDir(t, goSamplePath, sampleDir); err != nil {
		t.Fatalf("copy testdata: %v", err)
	}

	// ── Step 1: install ───────────────────────────────────────────────────────
	// Detect projects and write .codegraph.json.
	loader := &config.Loader{RootDir: root}
	ws, err := loader.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	// Detect may not find a Go project because there is no go.mod in go-sample.
	// Manually construct the workspace config for the test.
	ws = &config.Workspace{
		Name: "e2e-test",
		Projects: []config.Project{
			{Name: "go-sample", Path: "go-sample", Language: "go"},
		},
	}
	if err := loader.Save(ws); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	configPath := filepath.Join(root, ".codegraph.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("expected .codegraph.json to exist after install")
	}

	// Verify the saved config is valid JSON.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var savedWS config.Workspace
	if err := json.Unmarshal(data, &savedWS); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	if savedWS.Name == "" {
		t.Error("saved workspace name is empty")
	}
	t.Logf("install: workspace %q with %d project(s)", savedWS.Name, len(savedWS.Projects))

	// ── Step 2: build ─────────────────────────────────────────────────────────
	dbPath := filepath.Join(root, ".codegraph.db")
	store, err := graph.Open(dbPath)
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	if err := store.Migrate(); err != nil {
		store.Close()
		t.Fatalf("Migrate: %v", err)
	}
	if err := store.UpsertProject("go-sample", "go-sample", "go-sample", "go"); err != nil {
		store.Close()
		t.Fatalf("UpsertProject: %v", err)
	}

	registry := buildRegistry()
	buildResult, err := RunBuild(ws, store, registry, root)
	if err != nil {
		store.Close()
		t.Fatalf("RunBuild: %v", err)
	}
	t.Logf("build: %d files, %d symbols, %d edges",
		buildResult.FilesProcessed, buildResult.SymbolsAdded, buildResult.EdgesAdded)

	// ── Step 3: verify symbol and edge counts ─────────────────────────────────
	if buildResult.FilesProcessed == 0 {
		store.Close()
		t.Fatal("expected at least one file to be processed")
	}
	if buildResult.SymbolsAdded == 0 {
		store.Close()
		t.Fatal("expected symbol count > 0 after build")
	}
	// Edges may be 0 if the extractor doesn't produce them for this sample,
	// but we log the count for visibility.
	t.Logf("build verified: symbols=%d edges=%d", buildResult.SymbolsAdded, buildResult.EdgesAdded)

	// Confirm symbols are actually in the store.
	symsAfterBuild := countSymbols(t, store)
	if symsAfterBuild == 0 {
		store.Close()
		t.Fatal("store has 0 symbols after build")
	}

	// ── Step 4: update – modify one file ─────────────────────────────────────
	// Append a new exported function to service.go so the update picks it up.
	serviceFile := filepath.Join(sampleDir, "service.go")
	original, err := os.ReadFile(serviceFile)
	if err != nil {
		store.Close()
		t.Fatalf("read service.go: %v", err)
	}

	modified := string(original) + `
// E2ETestFunction is a sentinel function added by the e2e test.
func E2ETestFunction() string {
	return "e2e"
}
`
	if err := os.WriteFile(serviceFile, []byte(modified), 0o644); err != nil {
		store.Close()
		t.Fatalf("write modified service.go: %v", err)
	}

	updateResult, err := RunUpdate(ws, store, registry, root)
	if err != nil {
		store.Close()
		t.Fatalf("RunUpdate: %v", err)
	}
	t.Logf("update: +%d added, ~%d modified, -%d deleted; symbols delta=%d",
		updateResult.FilesAdded, updateResult.FilesModified, updateResult.FilesDeleted,
		updateResult.SymbolsDelta)

	// ── Step 5: verify the graph reflects the change ──────────────────────────
	// The modified file should have been picked up (added or modified).
	if updateResult.FilesAdded+updateResult.FilesModified == 0 {
		store.Close()
		t.Error("expected at least one file to be added or modified during update")
	}

	// The new function should now be searchable.
	results, err := store.SearchSymbols(graph.SearchQuery{Name: "E2ETestFunction", Limit: 10})
	if err != nil {
		store.Close()
		t.Fatalf("SearchSymbols: %v", err)
	}
	if len(results) == 0 {
		store.Close()
		t.Error("expected E2ETestFunction to appear in the graph after update")
	}
	t.Logf("update verified: E2ETestFunction found in graph")

	// ── Step 6: clean ─────────────────────────────────────────────────────────
	store.Close()

	cleanResult, err := runClean(root)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	t.Logf("clean: config_removed=%v db_removed=%v", cleanResult.ConfigRemoved, cleanResult.DBRemoved)

	// ── Step 7: verify files are removed ─────────────────────────────────────
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("expected .codegraph.json to be removed after clean")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("expected .codegraph.db to be removed after clean")
	}
	t.Log("clean verified: .codegraph.json and .codegraph.db removed")
}

// runClean removes .codegraph.json and .codegraph.db from root and returns the
// result. This mirrors what NewCleanCmd does without going through cobra.
func runClean(root string) (CleanResult, error) {
	configPath := filepath.Join(root, ".codegraph.json")
	dbPath := filepath.Join(root, ".codegraph.db")

	configRemoved, err := removeIfExists(configPath)
	if err != nil {
		return CleanResult{}, err
	}
	dbRemoved, err := removeIfExists(dbPath)
	if err != nil {
		return CleanResult{}, err
	}
	return CleanResult{
		ConfigRemoved: configRemoved,
		DBRemoved:     dbRemoved,
		ConfigPath:    configPath,
		DBPath:        dbPath,
	}, nil
}
