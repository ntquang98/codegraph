package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// CleanResult holds the outcome of a clean operation.
type CleanResult struct {
	ConfigRemoved bool   `json:"config_removed"`
	DBRemoved     bool   `json:"db_removed"`
	ConfigPath    string `json:"config_path"`
	DBPath        string `json:"db_path"`
}

// NewCleanCmd returns the cobra command for `codegraph clean`.
// It removes .codegraph.json and .codegraph.db from the workspace root,
// handling the case where either file does not exist gracefully
// (Requirement 1.10, 9.6).
func NewCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clean",
		Short: "Remove .codegraph.json and .codegraph.db from the workspace root",
		Long: `Remove the workspace configuration file (.codegraph.json) and the graph
database (.codegraph.db) from the workspace root. Files that do not exist are
silently skipped.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFlag, _ := cmd.Root().PersistentFlags().GetString("output")

			// Determine workspace root: prefer --workspace flag, then CWD.
			root := ""
			if wsOverride, _ := cmd.Root().PersistentFlags().GetString("workspace"); wsOverride != "" {
				root = wsOverride
			} else {
				var err error
				root, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("get working directory: %w", err)
				}
			}

			configPath := filepath.Join(root, ".codegraph.json")
			dbPath := filepath.Join(root, ".codegraph.db")

			configRemoved, err := removeIfExists(configPath)
			if err != nil {
				return fmt.Errorf("remove %s: %w", configPath, err)
			}

			dbRemoved, err := removeIfExists(dbPath)
			if err != nil {
				return fmt.Errorf("remove %s: %w", dbPath, err)
			}

			result := CleanResult{
				ConfigRemoved: configRemoved,
				DBRemoved:     dbRemoved,
				ConfigPath:    configPath,
				DBPath:        dbPath,
			}

			if outputFlag == "json" {
				return printCleanResultJSON(cmd.OutOrStdout(), result)
			}
			printCleanResultText(cmd.OutOrStdout(), result)
			return nil
		},
	}
}

// printCleanResultText prints a human-readable clean summary.
func printCleanResultText(w io.Writer, r CleanResult) {
	if r.ConfigRemoved {
		fmt.Fprintf(w, "Removed %s\n", r.ConfigPath)
	} else {
		fmt.Fprintf(w, "Not found (skipped): %s\n", r.ConfigPath)
	}
	if r.DBRemoved {
		fmt.Fprintf(w, "Removed %s\n", r.DBPath)
	} else {
		fmt.Fprintf(w, "Not found (skipped): %s\n", r.DBPath)
	}
}

// printCleanResultJSON prints a JSON clean summary.
func printCleanResultJSON(w io.Writer, r CleanResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func init() {
	rootCmd.AddCommand(NewCleanCmd())
}
