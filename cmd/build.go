package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codegraph-cli/codegraph/internal/config"
	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/parse"
	"github.com/codegraph-cli/codegraph/internal/walker"
	"github.com/spf13/cobra"
)

// BuildResult holds the summary statistics from a build run.
type BuildResult struct {
	SymbolsAdded   int
	EdgesAdded     int
	FilesProcessed int
	Errors         []error
}

// fileWork is a unit of work dispatched to a worker goroutine.
type fileWork struct {
	filePath  string
	projectID string
	wsRoot    string
}

// fileResult is the output produced by a worker goroutine for one file.
type fileResult struct {
	filePath  string
	hash      string
	symbols   []parse.Symbol
	edges     []parse.Edge
	err       error
}

// NewBuildCmd returns the cobra command for `codegraph build`.
func NewBuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build",
		Short: "Build the code graph from the workspace",
		Long:  "Parse all source files in the workspace and store the resulting symbols and edges in the graph database.",
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFlag, _ := cmd.Root().PersistentFlags().GetString("output")
			verbose, _ := cmd.Root().PersistentFlags().GetBool("verbose")

			// Build a logger that writes to stderr. When not verbose, discard output.
			var logger *log.Logger
			if verbose {
				logger = log.New(os.Stderr, "[codegraph] ", log.Ltime)
			} else {
				logger = log.New(io.Discard, "", 0)
			}

			logger.Println("loading workspace config...")

			// Load workspace config.
			loader := &config.Loader{}
			ws, err := loader.Load()
			if err != nil {
				return err
			}
			logger.Printf("workspace root: %s", loader.RootDir)

			// Open and migrate the graph store.
			dbPath := filepath.Join(loader.RootDir, ".codegraph.db")
			logger.Printf("opening graph store: %s", dbPath)
			store, err := graph.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			logger.Println("running database migrations...")
			if err := store.Migrate(); err != nil {
				return fmt.Errorf("migrate graph store: %w", err)
			}

			// Ensure all projects are registered in the store.
			if err := upsertProjects(store, ws, loader.RootDir); err != nil {
				return fmt.Errorf("register projects: %w", err)
			}

			// Build the parser registry.
			registry := buildRegistry()

			// Run the build.
			result, err := RunBuild(ws, store, registry, loader.RootDir, logger)
			if err != nil {
				return err
			}

			// Print summary.
			if outputFlag == "json" {
				return printBuildResultJSON(cmd.OutOrStdout(), result)
			}
			printBuildResultText(cmd.OutOrStdout(), result)
			return nil
		},
	}
}

// RunBuild executes the full build pipeline:
//  1. Flatten projects + services into a single list.
//  2. Walk each project's files.
//  3. Hash-compare each file against the stored hash.
//  4. For changed/new files: delete stale data, parse, upsert in batches of 500.
//
// Files are parsed concurrently using a worker pool bounded by runtime.NumCPU().
// Each file's symbols and edges are written in a single atomic transaction
// (via UpsertFileData) to satisfy Requirement 4.4 / task 6.4.
func RunBuild(ws *config.Workspace, store *graph.Store, registry *parse.Registry, wsRoot string, logger *log.Logger) (BuildResult, error) {
	var result BuildResult

	projects := flattenProjects(ws)
	supportedExts := registry.SupportedExtensions()

	logger.Printf("found %d project(s) to process", len(projects))

	// Collect all (filePath, projectID) pairs that need processing.
	type fileEntry struct {
		path      string
		projectID string
	}
	var allFiles []fileEntry

	for _, proj := range projects {
		absPath := filepath.Join(wsRoot, proj.Path)
		// Validate that the project path does not escape the workspace root
		// (Requirement 11.2, 11.3).
		if err := ValidatePathWithinWorkspace(wsRoot, absPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping project %q: %v\n", proj.Name, err)
			continue
		}
		logger.Printf("walking project %q at %s ...", proj.Name, absPath)
		walkStart := time.Now()
		files, err := walker.Walk(absPath, mergeExcludes(proj.Exclude), supportedExts)
		if err != nil {
			return result, fmt.Errorf("walk project %q: %w", proj.Name, err)
		}
		logger.Printf("  found %d file(s) in %s", len(files), time.Since(walkStart).Round(time.Millisecond))
		projID := projectID(proj)
		for _, f := range files {
			allFiles = append(allFiles, fileEntry{path: f, projectID: projID})
		}
	}

	logger.Printf("total files found: %d", len(allFiles))

	// Determine which files need re-parsing (hash changed or new).
	type workItem struct {
		path      string
		projectID string
		src       []byte
		hash      string
	}
	var workItems []workItem

	logger.Println("checking file hashes for changes...")
	hashStart := time.Now()

	// Load the entire file index in one query to avoid N individual DB round-trips.
	fileIndex, err := store.GetAllFileIndex()
	if err != nil {
		return result, fmt.Errorf("load file index: %w", err)
	}
	logger.Printf("  loaded %d tracked file(s) from index", len(fileIndex))

	// hashCheck is the result of checking one file.
	type hashCheckResult struct {
		path      string
		projectID string
		src       []byte
		hash      string
		skip      bool
		err       error
	}

	total := len(allFiles)
	hashWorkCh := make(chan fileEntry, total)

	// Progress reporter: logs every 5 s while the hash workers are running.
	progressDone := make(chan struct{})
	progressStop := make(chan struct{})
	var checkedCount int64
	go func() {
		defer close(progressDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				n := atomic.LoadInt64(&checkedCount)
				logger.Printf("  hash check progress: %d/%d files checked...", n, total)
			case <-progressStop:
				return
			}
		}
	}()

	// Spin up hash-check workers (same count as CPU workers).
	numHashWorkers := runtime.NumCPU()
	if numHashWorkers < 1 {
		numHashWorkers = 1
	}
	var hashWg sync.WaitGroup
	// Collect results into a slice so we can close progressDone cleanly.
	rawHashResults := make([]hashCheckResult, 0, total)
	rawHashMu := sync.Mutex{}

	for i := 0; i < numHashWorkers; i++ {
		hashWg.Add(1)
		go func() {
			defer hashWg.Done()
			localCount := int64(0)
			for fe := range hashWorkCh {
				entry, known := fileIndex[fe.path]

				// Fast path: if mod time matches the stored value, the file is
				// unchanged — no need to read or hash it.
				if known {
					info, statErr := os.Stat(fe.path)
					if statErr == nil && info.ModTime().UTC().Truncate(time.Second).Equal(
						entry.ModTime.UTC().Truncate(time.Second)) {
						localCount++
						if localCount%10_000 == 0 {
							atomic.AddInt64(&checkedCount, localCount)
							localCount = 0
						}
						rawHashMu.Lock()
						rawHashResults = append(rawHashResults, hashCheckResult{skip: true})
						rawHashMu.Unlock()
						continue
					}
				}

				// Slow path: read file and compute SHA-256.
				src, readErr := os.ReadFile(fe.path)
				if readErr != nil {
					localCount++
					rawHashMu.Lock()
					rawHashResults = append(rawHashResults, hashCheckResult{
						path: fe.path, err: fmt.Errorf("read %s: %w", fe.path, readErr),
					})
					rawHashMu.Unlock()
					continue
				}
				currentHash := hashBytes(src)

				localCount++
				if localCount%10_000 == 0 {
					atomic.AddInt64(&checkedCount, localCount)
					localCount = 0
				}

				if known && currentHash == entry.Hash {
					rawHashMu.Lock()
					rawHashResults = append(rawHashResults, hashCheckResult{skip: true})
					rawHashMu.Unlock()
					continue
				}

				rawHashMu.Lock()
				rawHashResults = append(rawHashResults, hashCheckResult{
					path:      fe.path,
					projectID: fe.projectID,
					src:       src,
					hash:      currentHash,
				})
				rawHashMu.Unlock()
			}
			// Flush remaining count.
			if localCount > 0 {
				atomic.AddInt64(&checkedCount, localCount)
			}
		}()
	}

	// Feed work.
	for _, fe := range allFiles {
		hashWorkCh <- fe
	}
	close(hashWorkCh)
	hashWg.Wait()
	close(progressStop)
	<-progressDone

	// Collect results.
	for _, r := range rawHashResults {
		if r.err != nil {
			result.Errors = append(result.Errors, r.err)
			continue
		}
		if r.skip {
			continue
		}
		workItems = append(workItems, workItem{
			path:      r.path,
			projectID: r.projectID,
			src:       r.src,
			hash:      r.hash,
		})
	}

	logger.Printf("hash check done in %s: %d/%d file(s) need parsing",
		time.Since(hashStart).Round(time.Millisecond), len(workItems), total)

	if len(workItems) == 0 {
		logger.Println("nothing to do — all files are up to date")
		return result, nil
	}

	// Parse files concurrently.
	numWorkers := runtime.NumCPU()
	if numWorkers < 1 {
		numWorkers = 1
	}
	logger.Printf("parsing %d file(s) with %d worker(s)...", len(workItems), numWorkers)
	parseStart := time.Now()

	type parseResult struct {
		path      string
		projectID string
		hash      string
		symbols   []parse.Symbol
		edges     []parse.Edge
		err       error
	}

	workCh := make(chan workItem, len(workItems))
	resultCh := make(chan parseResult, len(workItems))

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workCh {
				logger.Printf("  parsing %s", item.path)
				syms, edges, err := registry.ExtractFile(item.path, item.src)
				if err != nil {
					resultCh <- parseResult{path: item.path, err: err}
					continue
				}
				// Tag symbols with project context.
				for i := range syms {
					syms[i].ProjectID = item.projectID
				}
				resultCh <- parseResult{
					path:      item.path,
					projectID: item.projectID,
					hash:      item.hash,
					symbols:   syms,
					edges:     edges,
				}
			}
		}()
	}

	// Feed work.
	for _, item := range workItems {
		workCh <- item
	}
	close(workCh)

	// Close result channel once all workers finish.
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results and write to store in batches of 500 symbols or 2000 edges,
	// whichever is hit first. Both symbols and edges are written in a single
	// atomic transaction via UpsertBatch to halve the number of fsync commits.
	const batchSymbols = 500
	const batchEdges = 2000
	var pendingSymbols []parse.Symbol
	var pendingEdges []parse.Edge
	var pendingFiles []struct {
		path string
		hash string
	}

	flush := func() error {
		if len(pendingSymbols) == 0 && len(pendingEdges) == 0 {
			return nil
		}
		logger.Printf("  flushing batch: %d symbols, %d edges", len(pendingSymbols), len(pendingEdges))
		if err := store.UpsertBatch(pendingSymbols, pendingEdges); err != nil {
			return fmt.Errorf("upsert batch: %w", err)
		}
		pendingSymbols = pendingSymbols[:0]
		pendingEdges = pendingEdges[:0]
		return nil
	}

	for pr := range resultCh {
		if pr.err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("parse %s: %w", pr.path, pr.err))
			continue
		}

		// Delete stale data for this file before inserting new data.
		if err := store.DeleteByFile(pr.path); err != nil {
			return result, fmt.Errorf("delete stale data for %s: %w", pr.path, err)
		}

		pendingSymbols = append(pendingSymbols, pr.symbols...)
		pendingEdges = append(pendingEdges, pr.edges...)
		pendingFiles = append(pendingFiles, struct {
			path string
			hash string
		}{pr.path, pr.hash})

		result.SymbolsAdded += len(pr.symbols)
		result.EdgesAdded += len(pr.edges)
		result.FilesProcessed++

		// Flush when either threshold is hit.
		if len(pendingSymbols) >= batchSymbols || len(pendingEdges) >= batchEdges {
			if err := flush(); err != nil {
				return result, err
			}
		}
	}
	logger.Printf("parsing done in %s", time.Since(parseStart).Round(time.Millisecond))

	// Final flush.
	if err := flush(); err != nil {
		return result, err
	}

	// Update file hashes after successful write.
	logger.Println("updating file hashes...")
	for _, pf := range pendingFiles {
		info, err := os.Stat(pf.path)
		if err != nil {
			continue
		}
		if err := store.SetFileHash(pf.path, pf.hash, info.ModTime()); err != nil {
			return result, fmt.Errorf("set file hash %s: %w", pf.path, err)
		}
	}

	return result, nil
}

// flattenProjects returns a flat list of all projects and their services.
func flattenProjects(ws *config.Workspace) []config.Project {
	var out []config.Project
	for _, p := range ws.Projects {
		if len(p.Services) > 0 {
			for _, svc := range p.Services {
				out = append(out, svc)
			}
		} else {
			out = append(out, p)
		}
	}
	return out
}

// projectID returns a stable identifier for a project.
func projectID(p config.Project) string {
	return p.Name
}

// hashBytes returns the hex-encoded SHA-256 hash of b.
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// buildRegistry creates a parse.Registry with all supported language extractors.
func buildRegistry() *parse.Registry {
	reg := parse.NewRegistry()
	reg.Register(&parse.GoExtractor{})
	reg.Register(&parse.TypeScriptExtractor{})
	reg.Register(&parse.JavaScriptExtractor{})
	reg.Register(&parse.PythonExtractor{})
	reg.Register(&parse.CSharpExtractor{})
	reg.Register(&parse.RustExtractor{})
	reg.Register(&parse.ZigExtractor{})
	return reg
}

// upsertProjects ensures all projects from the workspace are registered in the
// graph store's projects table.
func upsertProjects(store *graph.Store, ws *config.Workspace, wsRoot string) error {
	projects := flattenProjects(ws)
	for _, p := range projects {
		if err := store.UpsertProject(projectID(p), p.Name, p.Path, p.Language); err != nil {
			return fmt.Errorf("upsert project %q: %w", p.Name, err)
		}
	}
	return nil
}

// printBuildResultText prints a human-readable build summary.
func printBuildResultText(w io.Writer, result BuildResult) {
	fmt.Fprintf(w, "Build complete: %d symbols, %d edges across %d files\n",
		result.SymbolsAdded, result.EdgesAdded, result.FilesProcessed)
	for _, e := range result.Errors {
		fmt.Fprintf(w, "  warning: %v\n", e)
	}
}

// printBuildResultJSON prints a JSON build summary.
func printBuildResultJSON(w io.Writer, result BuildResult) error {
	type jsonResult struct {
		SymbolsAdded   int      `json:"symbols_added"`
		EdgesAdded     int      `json:"edges_added"`
		FilesProcessed int      `json:"files_processed"`
		Errors         []string `json:"errors,omitempty"`
	}
	jr := jsonResult{
		SymbolsAdded:   result.SymbolsAdded,
		EdgesAdded:     result.EdgesAdded,
		FilesProcessed: result.FilesProcessed,
	}
	for _, e := range result.Errors {
		jr.Errors = append(jr.Errors, e.Error())
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jr)
}

func init() {
	rootCmd.AddCommand(NewBuildCmd())
}

// mergeExcludes returns the union of the project's own exclude patterns and the
// default exclude patterns, deduplicating entries so patterns aren't applied twice.
func mergeExcludes(projectExcludes []string) []string {
	seen := make(map[string]struct{}, len(projectExcludes)+len(config.DefaultExcludePatterns))
	result := make([]string, 0, len(projectExcludes)+len(config.DefaultExcludePatterns))
	for _, p := range projectExcludes {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			result = append(result, p)
		}
	}
	for _, p := range config.DefaultExcludePatterns {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			result = append(result, p)
		}
	}
	return result
}
