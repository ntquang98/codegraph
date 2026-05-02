package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/codegraph-cli/codegraph/internal/config"
	"github.com/spf13/cobra"
)

// NewInstallCmd returns the cobra command for `codegraph install`.
// It auto-detects projects in the workspace, prints the detected structure,
// and writes a .codegraph.json config file (Requirement 1.1, 1.2).
func NewInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Initialize workspace configuration by auto-detecting projects",
		Long: `Scan the current directory tree for recognizable project manifest files
(go.mod, package.json, *.csproj, pyproject.toml) and generate a .codegraph.json
file at the workspace root.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFlag, _ := cmd.Root().PersistentFlags().GetString("output")

			loader := &config.Loader{}

			// Allow --workspace override.
			if wsOverride, _ := cmd.Root().PersistentFlags().GetString("workspace"); wsOverride != "" {
				loader.RootDir = wsOverride
			}

			// Auto-detect projects from the filesystem.
			ws, err := loader.Detect()
			if err != nil {
				return fmt.Errorf("detect workspace: %w", err)
			}

			// Print detected structure.
			if outputFlag == "json" {
				if err := printInstallResultJSON(cmd.OutOrStdout(), ws, loader.ConfigPath()); err != nil {
					return err
				}
			} else {
				printInstallResultText(cmd.OutOrStdout(), ws)
			}

			// Write the config file.
			if err := loader.Save(ws); err != nil {
				return fmt.Errorf("save config: %w", err)
			}

			if outputFlag != "json" {
				fmt.Fprintf(cmd.OutOrStdout(), "Config written to %s\n", loader.ConfigPath())
			}

			return nil
		},
	}
}

// printInstallResultText prints a human-readable summary of the detected workspace.
func printInstallResultText(w io.Writer, ws *config.Workspace) {
	fmt.Fprintf(w, "Detected workspace: %s\n", ws.Name)
	if len(ws.Projects) == 0 {
		fmt.Fprintln(w, "  No projects detected.")
		return
	}
	for _, p := range ws.Projects {
		fmt.Fprintf(w, "  - %s (%s) [%s]\n", p.Name, p.Path, p.Language)
		for _, svc := range p.Services {
			fmt.Fprintf(w, "      - %s (%s) [%s]\n", svc.Name, svc.Path, svc.Language)
		}
	}
}

// printInstallResultJSON prints a JSON summary of the detected workspace and config path.
func printInstallResultJSON(w io.Writer, ws *config.Workspace, configPath string) error {
	type jsonResult struct {
		Workspace  *config.Workspace `json:"workspace"`
		ConfigPath string            `json:"config_path"`
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(jsonResult{Workspace: ws, ConfigPath: configPath})
}

func init() {
	rootCmd.AddCommand(NewInstallCmd())
}

// removeIfExists removes a file at path if it exists.
// Returns nil if the file does not exist (graceful handling per Requirement 1.10).
func removeIfExists(path string) (bool, error) {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
