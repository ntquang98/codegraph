package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/codegraph-cli/codegraph/internal/config"
	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/parse"
	"github.com/codegraph-cli/codegraph/internal/walker"
	"github.com/spf13/cobra"
)

// UpdateResult holds the summary statistics from an incremental update.
type UpdateResult struct {
	FilesAdded    int
	FilesModified int
	FilesDeleted  int
	SymbolsDelta  int // net change (positive = added, negative = removed)
	EdgesDelta    int
	Errors        []error
}

// NewUpdateCmd returns the cobra command for `codegraph update`.
func NewUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Incrementally update the code graph",
		Long:  "Compare the current workspace files against the stored file index and update only the changed files.",
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFlag, _ := cmd.Root().PersistentFlags().GetString("output")

			// Load workspace config.
			loader := &config.Loader{}
			ws, err := loader.Load()
			if err != nil {
				return err
			}

			// Open the existing graph store (must already exist from a prior build).
			dbPath := filepath.Join(loader.RootDir, ".codegraph.db")
			store, err := graph.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			if err := store.Migrate(); err != nil {
				return fmt.Errorf("migrate graph store: %w", err)
			}

			// Ensure all projects are registered.
			if err := upsertProjects(store, ws, loader.RootDir); err != nil {
				return fmt.Errorf("register projects: %w", err)
			}

			registry := buildRegistry()

			result, err := RunUpdate(ws, store, registry, loader.RootDir)
			if err != nil {
				return err
			}

			if outputFlag == "json" {
				return printUpdateResultJSON(cmd.OutOrStdout(), result)
			}
			printUpdateResultText(cmd.OutOrStdout(), result)
			return nil
		},
	}
}

// RunUpdate performs an incremental graph update:
//  1. Walk all current project files.
//  2. Get all previously tracked files from the store.
//  3. Compute added, modified, and deleted sets.
//  4. Process deletions first, then additions/modifications.
//
// The resulting graph state is equivalent to a full build on the current workspace
// (Property 2 / Requirement 5.5).
func RunUpdate(ws *config.Workspace, store *graph.Store, registry *parse.Registry, wsRoot string) (UpdateResult, error) {
	var result UpdateResult

	projects := flattenProjects(ws)
	supportedExts := registry.SupportedExtensions()

	// Walk all current files.
	currentFiles := make(map[string]string) // path → projectID
	for _, proj := range projects {
		absPath := filepath.Join(wsRoot, proj.Path)
		// Validate that the project path does not escape the workspace root
		// (Requirement 11.2, 11.3).
		if err := ValidatePathWithinWorkspace(wsRoot, absPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping project %q: %v\n", proj.Name, err)
			continue
		}
		files, err := walker.Walk(absPath, proj.Exclude, supportedExts)
		if err != nil {
			return result, fmt.Errorf("walk project %q: %w", proj.Name, err)
		}
		projID := projectID(proj)
		for _, f := range files {
			currentFiles[f] = projID
		}
	}

	// Get all previously tracked files.
	trackedFiles, err := store.GetAllTrackedFiles()
	if err != nil {
		return result, fmt.Errorf("get tracked files: %w", err)
	}
	trackedSet := make(map[string]struct{}, len(trackedFiles))
	for _, f := range trackedFiles {
		trackedSet[f] = struct{}{}
	}

	// Compute deleted files (tracked but no longer on disk or in scope).
	var deleted []string
	for _, f := range trackedFiles {
		if _, exists := currentFiles[f]; !exists {
			deleted = append(deleted, f)
		}
	}

	// Compute added and modified files.
	type modifiedFile struct {
		path      string
		projectID string
		src       []byte
		hash      string
	}
	var toProcess []modifiedFile

	for path, projID := range currentFiles {
		src, err := os.ReadFile(path)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("read %s: %w", path, err))
			continue
		}
		currentHash := hashBytes(src)
		storedHash, _, err := store.GetFileHash(path)
		if err != nil {
			return result, fmt.Errorf("get file hash %s: %w", path, err)
		}

		if _, tracked := trackedSet[path]; !tracked {
			// New file.
			result.FilesAdded++
			toProcess = append(toProcess, modifiedFile{path: path, projectID: projID, src: src, hash: currentHash})
		} else if currentHash != storedHash {
			// Modified file.
			result.FilesModified++
			toProcess = append(toProcess, modifiedFile{path: path, projectID: projID, src: src, hash: currentHash})
		}
	}

	// Process deletions first.
	for _, path := range deleted {
		// Count symbols being removed for the delta.
		syms, err := store.SearchSymbols(graph.SearchQuery{File: path, Limit: 100000})
		if err == nil {
			result.SymbolsDelta -= len(syms)
		}
		if err := store.DeleteByFile(path); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("delete %s: %w", path, err))
			continue
		}
		result.FilesDeleted++
	}

	// Process additions and modifications.
	for _, mf := range toProcess {
		syms, edges, err := registry.ExtractFile(mf.path, mf.src)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("parse %s: %w", mf.path, err))
			continue
		}

		// Tag symbols with project context.
		for i := range syms {
			syms[i].ProjectID = mf.projectID
		}

		// Delete stale data before inserting new.
		if err := store.DeleteByFile(mf.path); err != nil {
			return result, fmt.Errorf("delete stale data for %s: %w", mf.path, err)
		}

		if err := store.UpsertSymbols(syms); err != nil {
			return result, fmt.Errorf("upsert symbols for %s: %w", mf.path, err)
		}
		if err := store.UpsertEdges(edges); err != nil {
			return result, fmt.Errorf("upsert edges for %s: %w", mf.path, err)
		}

		info, err := os.Stat(mf.path)
		if err != nil {
			return result, fmt.Errorf("stat %s: %w", mf.path, err)
		}
		if err := store.SetFileHash(mf.path, mf.hash, info.ModTime()); err != nil {
			return result, fmt.Errorf("set file hash %s: %w", mf.path, err)
		}

		result.SymbolsDelta += len(syms)
		result.EdgesDelta += len(edges)
	}

	return result, nil
}

// printUpdateResultText prints a human-readable update summary.
func printUpdateResultText(w io.Writer, result UpdateResult) {
	fmt.Fprintf(w, "Update complete: +%d added, ~%d modified, -%d deleted files\n",
		result.FilesAdded, result.FilesModified, result.FilesDeleted)
	fmt.Fprintf(w, "  Symbols delta: %+d, Edges delta: %+d\n",
		result.SymbolsDelta, result.EdgesDelta)
	for _, e := range result.Errors {
		fmt.Fprintf(w, "  warning: %v\n", e)
	}
}

// printUpdateResultJSON prints a JSON update summary.
func printUpdateResultJSON(w io.Writer, result UpdateResult) error {
	type jsonResult struct {
		FilesAdded    int      `json:"files_added"`
		FilesModified int      `json:"files_modified"`
		FilesDeleted  int      `json:"files_deleted"`
		SymbolsDelta  int      `json:"symbols_delta"`
		EdgesDelta    int      `json:"edges_delta"`
		Errors        []string `json:"errors,omitempty"`
	}
	jr := jsonResult{
		FilesAdded:    result.FilesAdded,
		FilesModified: result.FilesModified,
		FilesDeleted:  result.FilesDeleted,
		SymbolsDelta:  result.SymbolsDelta,
		EdgesDelta:    result.EdgesDelta,
	}
	for _, e := range result.Errors {
		jr.Errors = append(jr.Errors, e.Error())
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jr)
}

func init() {
	rootCmd.AddCommand(NewUpdateCmd())
}
