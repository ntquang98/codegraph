package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/codegraph-cli/codegraph/internal/config"
	"github.com/codegraph-cli/codegraph/internal/graph"
	"github.com/codegraph-cli/codegraph/internal/mcp"
	"github.com/spf13/cobra"
)

// localhostAddresses is the set of host values considered local-only.
var localhostAddresses = map[string]bool{
	"127.0.0.1": true,
	"localhost":  true,
	"::1":        true,
}

// NewAgentCmd returns the cobra command for `codegraph agent`.
// It has a single sub-command: `mcp`.
func NewAgentCmd() *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Start an agent server",
	}

	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the MCP (Model Context Protocol) server for AI agents",
		Long: `Start an HTTP/JSON-RPC 2.0 MCP server that exposes the code knowledge graph
as tools for AI coding agents (Claude, Cursor, etc.).

The server binds to 127.0.0.1 by default. Use --host to override (a warning
will be printed when binding to a non-localhost address).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			host, _ := cmd.Flags().GetString("host")
			port, _ := cmd.Flags().GetInt("port")

			// Warn when binding to a non-localhost address (Requirement 7.11).
			if !localhostAddresses[host] {
				fmt.Fprintf(os.Stderr,
					"WARNING: MCP server is binding to %s — this exposes your code graph to the network.\n", host)
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
			store, err := graph.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open graph store: %w", err)
			}
			defer store.Close()

			if err := store.Migrate(); err != nil {
				return fmt.Errorf("migrate graph store: %w", err)
			}

			srv := mcp.NewServer(store, addr)

			// Print the listening address before blocking (Requirement 7.1).
			fmt.Fprintf(cmd.OutOrStdout(), "MCP server listening on http://%s/mcp\n", addr)

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

	mcpCmd.Flags().String("host", "127.0.0.1", "Host address to bind the MCP server to")
	mcpCmd.Flags().Int("port", 3333, "Port to listen on")

	agentCmd.AddCommand(mcpCmd)
	return agentCmd
}

func init() {
	rootCmd.AddCommand(NewAgentCmd())
}
