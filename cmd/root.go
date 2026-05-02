package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "codegraph",
	Short: "A code knowledge graph CLI for multi-language repositories",
}

// Execute runs the root cobra command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().String("output", "text", "Output format: text or json")
	rootCmd.PersistentFlags().String("workspace", "", "Override workspace root directory")
}

// ValidatePathWithinWorkspace resolves absPath and verifies it is within
// wsRoot. Returns an error if the resolved path escapes the workspace root.
// This implements Requirement 11.2 and 11.3 (path traversal protection).
func ValidatePathWithinWorkspace(wsRoot, absPath string) error {
	// Resolve both paths to their canonical forms.
	resolvedRoot, err := filepath.EvalSymlinks(wsRoot)
	if err != nil {
		// If the root doesn't exist yet, fall back to Clean.
		resolvedRoot = filepath.Clean(wsRoot)
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Path may not exist yet; use Clean to normalize.
		resolvedPath = filepath.Clean(absPath)
	}

	// Ensure the resolved path starts with the workspace root.
	// Add a separator to prevent prefix attacks (e.g. /workspace-evil matching /workspace).
	rootWithSep := resolvedRoot
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}

	if resolvedPath != resolvedRoot && !strings.HasPrefix(resolvedPath, rootWithSep) {
		return fmt.Errorf("path %q escapes workspace root %q", absPath, wsRoot)
	}
	return nil
}

// ValidateProjectPaths checks all project paths in the workspace against the
// workspace root. It logs a warning and skips paths that escape the root,
// returning the list of safe absolute paths.
func ValidateProjectPaths(wsRoot string, projectPaths []string) ([]string, error) {
	var safe []string
	for _, rel := range projectPaths {
		abs := filepath.Join(wsRoot, rel)
		if err := ValidatePathWithinWorkspace(wsRoot, abs); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping project path: %v\n", err)
			continue
		}
		safe = append(safe, abs)
	}
	return safe, nil
}
