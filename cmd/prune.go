package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/codegraph-cli/codegraph/internal/config"
	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/spf13/cobra"
)

// NewPruneCmd returns the cobra command for `codegraph prune`.
func NewPruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Remove orphan edges from the graph database",
		Long: `Delete edges whose source or target symbol no longer exists in the database.

Orphan edges accumulate when files are deleted, renamed, or re-parsed with
different symbol IDs (e.g. after a schema change). Running prune restores
edge consistency without requiring a full rebuild.

prune is also run automatically at the end of every 'codegraph build'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			loader := &config.Loader{}
			if wsOverride, _ := cmd.Root().PersistentFlags().GetString("workspace"); wsOverride != "" {
				loader.RootDir = wsOverride
			}
			if _, err := loader.Load(); err != nil {
				return err
			}

			dbPath := filepath.Join(loader.RootDir, ".codegraph.db")
			store, err := graph.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			fmt.Fprintf(cmd.OutOrStdout(), "Pruning orphan edges from %s...\n", dbPath)
			n, err := store.PruneOrphanEdges()
			if err != nil {
				return fmt.Errorf("prune: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Done: removed %d orphan edge(s).\n", n)
			return nil
		},
	}
}

func init() {
	rootCmd.AddCommand(NewPruneCmd())
}
