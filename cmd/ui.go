package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/codegraph-cli/codegraph/internal/config"
	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/ui"
	"github.com/spf13/cobra"
)

// frontendFS holds the embedded frontend assets. It is set by main.go via
// SetFrontendFS before the root command is executed.
var frontendFS fs.FS

// SetFrontendFS injects the embedded frontend filesystem into the ui command.
// Call this from main.go after embedding the assets.
func SetFrontendFS(f fs.FS) { frontendFS = f }

// NewUICmd returns the cobra command for `codegraph ui`.
func NewUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Start the browser-based graph visualization UI",
		Long: `Start an HTTP server that serves the embedded graph visualization frontend.

The server binds to 127.0.0.1 by default. Use --host to override (a warning
will be printed when binding to a non-localhost address).

The default browser is opened automatically unless --no-open is provided.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			host, _   := cmd.Flags().GetString("host")
			port, _   := cmd.Flags().GetInt("port")
			noOpen, _ := cmd.Flags().GetBool("no-open")

			// Warn when binding to a non-localhost address (Requirement 8.10).
			if !localhostAddresses[host] {
				fmt.Fprintf(os.Stderr,
					"WARNING: UI server is binding to %s — this exposes your code graph to the network.\n", host)
			}

			addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))

			// Load workspace config to locate the database.
			loader := &config.Loader{}
			if wsOverride, _ := cmd.Root().PersistentFlags().GetString("workspace"); wsOverride != "" {
				loader.RootDir = wsOverride
			}
			if _, err := loader.Load(); err != nil {
				return err
			}

			dbPath := filepath.Join(loader.RootDir, ".codegraph.db")
			store, err := graph.OpenReadOnly(dbPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			// No Migrate() — the UI is read-only and the schema is managed by
			// `codegraph build`. Skipping Migrate avoids acquiring a write lock
			// which would block if another process has the DB open.

			// Print graph size stats so the user knows what they're loading.
			if symCount, err := store.CountSymbols(graph.SearchQuery{}); err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Graph database: %s\n", dbPath)
				fmt.Fprintf(cmd.OutOrStdout(), "  Symbols : %d\n", symCount)
				if edgeCount, err := store.CountEdges(); err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  Edges   : %d\n", edgeCount)
				}
				if validEdges, err := store.CountValidEdges(); err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  Valid edges (both endpoints exist): %d\n", validEdges)
				}
				if projStats, err := store.GetProjectStats(); err == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "  Projects: %d\n", len(projStats))
				}
				if symCount > 50000 {
					fmt.Fprintf(cmd.OutOrStdout(),
						"  ⚠  Large graph (%d symbols) — UI will paginate. Use project filter to focus.\n",
						symCount)
				}
			}

			if frontendFS == nil {
				return fmt.Errorf("frontend assets not initialised (call cmd.SetFrontendFS)")
			}

			srv := ui.NewServer(store, addr, frontendFS, noOpen)

			url := fmt.Sprintf("http://%s", addr)
			fmt.Fprintf(cmd.OutOrStdout(), "Graph UI available at %s\n", url)

			// Graceful shutdown on SIGINT / SIGTERM.
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

			errCh := make(chan error, 1)
			go func() {
				errCh <- srv.Start()
			}()

			select {
			case sig := <-quit:
				fmt.Fprintf(cmd.OutOrStdout(), "\nReceived %s, shutting down...\n", sig)
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				return srv.Stop(shutdownCtx)
			case err := <-errCh:
				return err
			}
		},
	}

	cmd.Flags().String("host",  "127.0.0.1", "Host address to bind the UI server to")
	cmd.Flags().Int("port",     8080,         "Port to listen on")
	cmd.Flags().Bool("no-open", false,        "Do not open the browser automatically")

	return cmd
}

func init() {
	rootCmd.AddCommand(NewUICmd())
}
