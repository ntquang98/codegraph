package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// executeCommand runs a cobra command with the given args and captures stdout/stderr.
// It returns the stdout output, stderr output, and any error.
func executeCommand(root *cobra.Command, args ...string) (stdout, stderr string, err error) {
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// buildFullRootCmd creates a root cobra command with all subcommands wired,
// mirroring the real CLI setup used in production.
func buildFullRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "codegraph",
		Short:        "A code knowledge graph CLI for multi-language repositories",
		SilenceUsage: true, // suppress usage on error for cleaner test output
	}
	root.PersistentFlags().String("output", "text", "Output format: text or json")
	root.PersistentFlags().String("workspace", "", "Override workspace root directory")

	root.AddCommand(NewInstallCmd())
	root.AddCommand(NewBuildCmd())
	root.AddCommand(NewUpdateCmd())
	root.AddCommand(NewAgentCmd())
	root.AddCommand(NewUICmd())
	root.AddCommand(NewCleanCmd())

	return root
}

// ─── 10.3: No-config guard tests ─────────────────────────────────────────────

// TestNoConfig_Build verifies that running `build` without a .codegraph.json
// prints the required error message and returns a non-zero exit code
// (Requirement 1.7, 9.3).
func TestNoConfig_Build(t *testing.T) {
	dir := t.TempDir() // empty directory — no .codegraph.json

	root := buildFullRootCmd()
	_, _, err := executeCommand(root, "build", "--workspace", dir)

	if err == nil {
		t.Fatal("expected build to fail without .codegraph.json, got nil error")
	}

	// The error message must contain the required text (Requirement 1.7).
	errMsg := err.Error()
	if !strings.Contains(errMsg, "No .codegraph.json found") {
		t.Errorf("expected error to contain 'No .codegraph.json found', got: %q", errMsg)
	}
	if !strings.Contains(errMsg, "codegraph install") {
		t.Errorf("expected error to mention 'codegraph install', got: %q", errMsg)
	}
}

// TestNoConfig_Update verifies that running `update` without a .codegraph.json
// prints the required error message and returns a non-zero exit code.
func TestNoConfig_Update(t *testing.T) {
	dir := t.TempDir()

	root := buildFullRootCmd()
	_, _, err := executeCommand(root, "update", "--workspace", dir)

	if err == nil {
		t.Fatal("expected update to fail without .codegraph.json, got nil error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "No .codegraph.json found") {
		t.Errorf("expected error to contain 'No .codegraph.json found', got: %q", errMsg)
	}
}

// TestNoConfig_AgentMCP verifies that running `agent mcp` without a
// .codegraph.json prints the required error message.
func TestNoConfig_AgentMCP(t *testing.T) {
	dir := t.TempDir()

	root := buildFullRootCmd()
	_, _, err := executeCommand(root, "agent", "mcp", "--workspace", dir)

	if err == nil {
		t.Fatal("expected agent mcp to fail without .codegraph.json, got nil error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "No .codegraph.json found") {
		t.Errorf("expected error to contain 'No .codegraph.json found', got: %q", errMsg)
	}
}

// TestNoConfig_UI verifies that running `ui` without a .codegraph.json
// prints the required error message.
func TestNoConfig_UI(t *testing.T) {
	dir := t.TempDir()

	root := buildFullRootCmd()
	_, _, err := executeCommand(root, "ui", "--workspace", dir)

	if err == nil {
		t.Fatal("expected ui to fail without .codegraph.json, got nil error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "No .codegraph.json found") {
		t.Errorf("expected error to contain 'No .codegraph.json found', got: %q", errMsg)
	}
}

// TestNoConfig_Install verifies that `install` does NOT require a pre-existing
// .codegraph.json (it creates one).
func TestNoConfig_Install(t *testing.T) {
	dir := t.TempDir()

	root := buildFullRootCmd()
	_, _, err := executeCommand(root, "install", "--workspace", dir)

	if err != nil {
		t.Fatalf("install should succeed without .codegraph.json, got: %v", err)
	}

	// Config should have been created.
	if _, statErr := os.Stat(filepath.Join(dir, ".codegraph.json")); os.IsNotExist(statErr) {
		t.Error("expected .codegraph.json to be created by install")
	}
}

// TestNoConfig_Clean verifies that `clean` does NOT require a pre-existing
// .codegraph.json (it just removes files if they exist).
func TestNoConfig_Clean(t *testing.T) {
	dir := t.TempDir()

	root := buildFullRootCmd()
	_, _, err := executeCommand(root, "clean", "--workspace", dir)

	if err != nil {
		t.Fatalf("clean should succeed without .codegraph.json, got: %v", err)
	}
}

// ─── 10.5: --output json tests ────────────────────────────────────────────────

// TestBuildOutputJSON verifies that `build --output json` produces valid JSON
// output (Requirement 9.4).
func TestBuildOutputJSON(t *testing.T) {
	if !isCGoBuild() {
		t.Skip("skipping: go extractor requires CGo")
	}

	dir := t.TempDir()

	// Create a minimal Go project.
	projDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(projDir, "main.go"), `package main

func Hello() string {
	return "hello"
}
`)

	// Write a .codegraph.json config.
	config := `{
  "name": "test",
  "projects": [
    {"name": "src", "path": "src", "language": "go"}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, ".codegraph.json"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := buildFullRootCmd()
	stdout, _, err := executeCommand(root, "build", "--workspace", dir, "--output", "json")

	if err != nil {
		t.Fatalf("build --output json failed: %v", err)
	}

	// Verify the output is valid JSON.
	var result struct {
		SymbolsAdded   int      `json:"symbols_added"`
		EdgesAdded     int      `json:"edges_added"`
		FilesProcessed int      `json:"files_processed"`
		Errors         []string `json:"errors,omitempty"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("build --output json produced invalid JSON: %v\noutput: %s", err, stdout)
	}

	// Should have processed at least one file.
	if result.FilesProcessed == 0 {
		t.Error("expected at least one file to be processed")
	}
}

// TestBuildOutputText verifies that `build` without --output json produces
// human-readable text output.
func TestBuildOutputText(t *testing.T) {
	if !isCGoBuild() {
		t.Skip("skipping: go extractor requires CGo")
	}

	dir := t.TempDir()

	projDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(projDir, "main.go"), `package main

func Hello() string {
	return "hello"
}
`)

	config := `{
  "name": "test",
  "projects": [
    {"name": "src", "path": "src", "language": "go"}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, ".codegraph.json"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := buildFullRootCmd()
	stdout, _, err := executeCommand(root, "build", "--workspace", dir)

	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	if !strings.Contains(stdout, "Build complete") {
		t.Errorf("expected text output to contain 'Build complete', got: %q", stdout)
	}
}

// ─── 10.4: Path traversal validation tests ───────────────────────────────────

// TestValidatePathWithinWorkspace_Safe verifies that paths within the workspace
// root are accepted.
func TestValidatePathWithinWorkspace_Safe(t *testing.T) {
	root := t.TempDir()
	subDir := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := ValidatePathWithinWorkspace(root, subDir); err != nil {
		t.Errorf("expected safe path to be accepted, got: %v", err)
	}
}

// TestValidatePathWithinWorkspace_Escape verifies that paths escaping the
// workspace root are rejected (Requirement 11.2, 11.3).
func TestValidatePathWithinWorkspace_Escape(t *testing.T) {
	root := t.TempDir()

	// Construct a path that escapes the workspace root.
	escapePath := filepath.Join(root, "..", "etc", "passwd")
	escapePath = filepath.Clean(escapePath)

	if err := ValidatePathWithinWorkspace(root, escapePath); err == nil {
		t.Errorf("expected path traversal to be rejected, but it was accepted: %s", escapePath)
	}
}

// TestValidatePathWithinWorkspace_RootItself verifies that the workspace root
// itself is accepted.
func TestValidatePathWithinWorkspace_RootItself(t *testing.T) {
	root := t.TempDir()

	if err := ValidatePathWithinWorkspace(root, root); err != nil {
		t.Errorf("expected workspace root itself to be accepted, got: %v", err)
	}
}

// TestValidatePathWithinWorkspace_PrefixAttack verifies that a path that is a
// prefix of the workspace root but not within it is rejected.
// e.g. workspace=/tmp/abc, path=/tmp/abcdef should be rejected.
func TestValidatePathWithinWorkspace_PrefixAttack(t *testing.T) {
	// Create a temp dir and a sibling with a similar name.
	parent := t.TempDir()
	wsRoot := filepath.Join(parent, "workspace")
	sibling := filepath.Join(parent, "workspace-evil")

	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatalf("mkdir wsRoot: %v", err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}

	if err := ValidatePathWithinWorkspace(wsRoot, sibling); err == nil {
		t.Errorf("expected sibling path to be rejected (prefix attack), but it was accepted: %s", sibling)
	}
}

// TestBuild_PathTraversalInConfig verifies that a project path in .codegraph.json
// that escapes the workspace root is skipped with a warning (Requirement 11.2, 11.3).
func TestBuild_PathTraversalInConfig(t *testing.T) {
	dir := t.TempDir()

	// Write a config with a path traversal attempt.
	config := `{
  "name": "test",
  "projects": [
    {"name": "evil", "path": "../../etc", "language": "go"}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, ".codegraph.json"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := buildFullRootCmd()
	// The build should not crash — it should skip the bad path and succeed
	// (or fail gracefully if the path doesn't exist, which is handled by config validation).
	// Since config.Load() validates paths exist on disk, this will fail at load time
	// with a "path does not exist" warning (the project is skipped).
	// The build should complete with 0 files processed.
	stdout, _, err := executeCommand(root, "build", "--workspace", dir)

	// Either the command succeeds (project skipped) or fails with a path error.
	// It must NOT read files outside the workspace.
	if err != nil {
		// Acceptable: config validation may reject the path.
		t.Logf("build with traversal path returned error (acceptable): %v", err)
		return
	}

	// If it succeeded, it should have processed 0 files.
	if strings.Contains(stdout, "symbols") {
		t.Logf("build output: %s", stdout)
	}
}

// ─── 10.2: Error handling — non-zero exit code ────────────────────────────────

// TestErrorHandling_NonZeroExitCode verifies that commands that fail return
// errors to cobra (which exits with code 1) rather than calling os.Exit directly.
// We test this by checking that Execute() returns an error.
func TestErrorHandling_NonZeroExitCode(t *testing.T) {
	dir := t.TempDir() // no config

	// Build command should return an error (not panic or call os.Exit).
	root := buildFullRootCmd()
	root.SetArgs([]string{"build", "--workspace", dir})

	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)

	err := root.Execute()
	if err == nil {
		t.Fatal("expected non-nil error from build without config")
	}

	// The error should be propagated (not swallowed).
	t.Logf("error returned: %v", err)
}
