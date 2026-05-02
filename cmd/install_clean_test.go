package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codegraph-cli/codegraph/internal/config"
	"github.com/spf13/cobra"
)

// ─── Install tests (task 9.3) ─────────────────────────────────────────────────

// TestInstall_DetectGoProject verifies that install detects a Go project and
// writes a valid .codegraph.json.
func TestInstall_DetectGoProject(t *testing.T) {
	root := t.TempDir()

	// Create a go.mod so Detect() finds a Go project.
	goMod := "module example.com/myapp\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewInstallCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	// Wire the root command so PersistentFlags are available.
	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"install", "--workspace", root})

	if err := root2.Execute(); err != nil {
		t.Fatalf("install command failed: %v", err)
	}

	// Verify .codegraph.json was written.
	configPath := filepath.Join(root, ".codegraph.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("expected .codegraph.json to be written, got error: %v", err)
	}

	var ws config.Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		t.Fatalf("written config is not valid JSON: %v", err)
	}

	// Should have detected at least one Go project.
	found := false
	for _, p := range ws.Projects {
		if p.Language == "go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a Go project in detected workspace, got: %+v", ws.Projects)
	}
}

// TestInstall_DetectProducesValidWorkspace verifies that the detected workspace
// has a non-empty name and all projects have non-empty names and valid languages.
func TestInstall_DetectProducesValidWorkspace(t *testing.T) {
	root := t.TempDir()

	// Create multiple project types.
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tsDir := filepath.Join(root, "frontend")
	if err := os.MkdirAll(tsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tsDir, "package.json"), []byte(`{"name":"frontend"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tsDir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := &config.Loader{RootDir: root}
	ws, err := loader.Detect()
	if err != nil {
		t.Fatalf("Detect() failed: %v", err)
	}

	if ws.Name == "" {
		t.Error("detected workspace name must be non-empty")
	}

	for _, p := range ws.Projects {
		if p.Name == "" {
			t.Errorf("project has empty name: %+v", p)
		}
		if p.Language == "" {
			t.Errorf("project %q has empty language", p.Name)
		}
		if p.Path == "" {
			t.Errorf("project %q has empty path", p.Name)
		}
	}
}

// TestInstall_SaveWritesCorrectJSON verifies that after install, the written
// JSON can be round-tripped back through Load.
func TestInstall_SaveWritesCorrectJSON(t *testing.T) {
	root := t.TempDir()

	// Create a Python project.
	if err := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[tool.poetry]\nname=\"app\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := &config.Loader{RootDir: root}
	ws, err := loader.Detect()
	if err != nil {
		t.Fatalf("Detect() failed: %v", err)
	}

	if err := loader.Save(ws); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Verify the file is valid JSON.
	data, err := os.ReadFile(loader.ConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var decoded config.Workspace
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("saved config is not valid JSON: %v", err)
	}

	if decoded.Name != ws.Name {
		t.Errorf("name mismatch: saved %q, decoded %q", ws.Name, decoded.Name)
	}
}

// TestInstall_OutputText verifies the text output of the install command
// includes the workspace name and project info.
func TestInstall_OutputText(t *testing.T) {
	root := t.TempDir()

	goMod := "module example.com/myapp\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := NewInstallCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"install", "--workspace", root})

	if err := root2.Execute(); err != nil {
		t.Fatalf("install command failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Detected workspace") {
		t.Errorf("expected output to contain 'Detected workspace', got: %q", output)
	}
	if !strings.Contains(output, "Config written to") {
		t.Errorf("expected output to contain 'Config written to', got: %q", output)
	}
}

// TestInstall_OutputJSON verifies the JSON output of the install command.
func TestInstall_OutputJSON(t *testing.T) {
	root := t.TempDir()

	goMod := "module example.com/myapp\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := NewInstallCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"install", "--workspace", root, "--output", "json"})

	if err := root2.Execute(); err != nil {
		t.Fatalf("install command failed: %v", err)
	}

	var result struct {
		Workspace  config.Workspace `json:"workspace"`
		ConfigPath string           `json:"config_path"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out.String())
	}

	if result.ConfigPath == "" {
		t.Error("expected config_path in JSON output")
	}
}

// TestInstall_EmptyDirectory verifies that install on an empty directory
// produces a workspace with no projects but still writes the config.
func TestInstall_EmptyDirectory(t *testing.T) {
	root := t.TempDir()

	cmd := NewInstallCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"install", "--workspace", root})

	if err := root2.Execute(); err != nil {
		t.Fatalf("install on empty directory failed: %v", err)
	}

	// Config should still be written.
	if _, err := os.Stat(filepath.Join(root, ".codegraph.json")); os.IsNotExist(err) {
		t.Error("expected .codegraph.json to be written even for empty directory")
	}
}

// ─── Clean tests (task 9.3) ───────────────────────────────────────────────────

// TestClean_RemovesBothFiles verifies that clean removes both .codegraph.json
// and .codegraph.db when they exist.
func TestClean_RemovesBothFiles(t *testing.T) {
	root := t.TempDir()

	configPath := filepath.Join(root, ".codegraph.json")
	dbPath := filepath.Join(root, ".codegraph.db")

	// Create both files.
	if err := os.WriteFile(configPath, []byte(`{"name":"ws","projects":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("fake db"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"clean", "--workspace", root})

	if err := root2.Execute(); err != nil {
		t.Fatalf("clean command failed: %v", err)
	}

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("expected .codegraph.json to be removed")
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("expected .codegraph.db to be removed")
	}
}

// TestClean_NonExistentFilesNoError verifies that clean does not return an
// error when neither file exists (Requirement 1.10).
func TestClean_NonExistentFilesNoError(t *testing.T) {
	root := t.TempDir()
	// Neither file exists.

	cmd := NewCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"clean", "--workspace", root})

	if err := root2.Execute(); err != nil {
		t.Fatalf("clean on non-existent files should not error, got: %v", err)
	}
}

// TestClean_OnlyConfigExists verifies that clean handles the case where only
// .codegraph.json exists (no .codegraph.db).
func TestClean_OnlyConfigExists(t *testing.T) {
	root := t.TempDir()

	configPath := filepath.Join(root, ".codegraph.json")
	if err := os.WriteFile(configPath, []byte(`{"name":"ws","projects":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"clean", "--workspace", root})

	if err := root2.Execute(); err != nil {
		t.Fatalf("clean failed: %v", err)
	}

	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Error("expected .codegraph.json to be removed")
	}
}

// TestClean_OnlyDBExists verifies that clean handles the case where only
// .codegraph.db exists (no .codegraph.json).
func TestClean_OnlyDBExists(t *testing.T) {
	root := t.TempDir()

	dbPath := filepath.Join(root, ".codegraph.db")
	if err := os.WriteFile(dbPath, []byte("fake db"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewCleanCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"clean", "--workspace", root})

	if err := root2.Execute(); err != nil {
		t.Fatalf("clean failed: %v", err)
	}

	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Error("expected .codegraph.db to be removed")
	}
}

// TestClean_OutputText verifies the text output of the clean command.
func TestClean_OutputText(t *testing.T) {
	root := t.TempDir()

	configPath := filepath.Join(root, ".codegraph.json")
	dbPath := filepath.Join(root, ".codegraph.db")
	if err := os.WriteFile(configPath, []byte(`{"name":"ws","projects":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("fake db"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := NewCleanCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"clean", "--workspace", root})

	if err := root2.Execute(); err != nil {
		t.Fatalf("clean failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Removed") {
		t.Errorf("expected output to contain 'Removed', got: %q", output)
	}
}

// TestClean_OutputJSON verifies the JSON output of the clean command.
func TestClean_OutputJSON(t *testing.T) {
	root := t.TempDir()

	configPath := filepath.Join(root, ".codegraph.json")
	dbPath := filepath.Join(root, ".codegraph.db")
	if err := os.WriteFile(configPath, []byte(`{"name":"ws","projects":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("fake db"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := NewCleanCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"clean", "--workspace", root, "--output", "json"})

	if err := root2.Execute(); err != nil {
		t.Fatalf("clean failed: %v", err)
	}

	var result CleanResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out.String())
	}

	if !result.ConfigRemoved {
		t.Error("expected config_removed to be true")
	}
	if !result.DBRemoved {
		t.Error("expected db_removed to be true")
	}
}

// TestClean_SkippedFilesReportedInOutput verifies that skipped (non-existent)
// files are reported in the output rather than silently ignored.
func TestClean_SkippedFilesReportedInOutput(t *testing.T) {
	root := t.TempDir()
	// Neither file exists.

	var out bytes.Buffer
	cmd := NewCleanCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})

	root2 := buildTestRootCmd()
	root2.AddCommand(cmd)
	root2.SetArgs([]string{"clean", "--workspace", root})

	if err := root2.Execute(); err != nil {
		t.Fatalf("clean failed: %v", err)
	}

	output := out.String()
	if !strings.Contains(output, "Not found") {
		t.Errorf("expected output to mention 'Not found' for missing files, got: %q", output)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// buildTestRootCmd creates a minimal root cobra command with the persistent
// flags that install/clean depend on (--output, --workspace).
func buildTestRootCmd() *cobra.Command {
	root := &cobra.Command{Use: "codegraph"}
	root.PersistentFlags().String("output", "text", "Output format: text or json")
	root.PersistentFlags().String("workspace", "", "Override workspace root directory")
	return root
}
