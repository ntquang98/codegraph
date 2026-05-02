package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// writeConfig writes a .codegraph.json file with the given content to dir.
func writeConfig(t *testing.T, dir string, content string) {
	t.Helper()
	path := filepath.Join(dir, configFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
}

// ---- Load tests ----

func TestLoad_ValidConfig(t *testing.T) {
	dir := t.TempDir()

	// Create the project directory referenced in the config
	projDir := filepath.Join(dir, "myservice")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := `{
		"name": "myworkspace",
		"projects": [
			{"name": "myservice", "path": "myservice", "language": "go"}
		]
	}`
	writeConfig(t, dir, cfg)

	loader := &Loader{RootDir: dir}
	ws, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() returned unexpected error: %v", err)
	}

	if ws.Name != "myworkspace" {
		t.Errorf("expected workspace name %q, got %q", "myworkspace", ws.Name)
	}
	if len(ws.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(ws.Projects))
	}
	if ws.Projects[0].Name != "myservice" {
		t.Errorf("expected project name %q, got %q", "myservice", ws.Projects[0].Name)
	}
	if ws.Projects[0].Language != "go" {
		t.Errorf("expected language %q, got %q", "go", ws.Projects[0].Language)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	dir := t.TempDir()

	loader := &Loader{RootDir: dir}
	_, err := loader.Load()
	if err == nil {
		t.Fatal("Load() expected error for missing config, got nil")
	}
	if !strings.Contains(err.Error(), "codegraph install") {
		t.Errorf("expected error to mention 'codegraph install', got: %v", err)
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `{not valid json}`)

	loader := &Loader{RootDir: dir}
	_, err := loader.Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid JSON, got nil")
	}
}

func TestLoad_InvalidLanguage(t *testing.T) {
	dir := t.TempDir()

	projDir := filepath.Join(dir, "svc")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := `{
		"name": "ws",
		"projects": [
			{"name": "svc", "path": "svc", "language": "cobol"}
		]
	}`
	writeConfig(t, dir, cfg)

	loader := &Loader{RootDir: dir}
	_, err := loader.Load()
	if err == nil {
		t.Fatal("Load() expected error for invalid language, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported language") {
		t.Errorf("expected error to mention 'unsupported language', got: %v", err)
	}
}

func TestLoad_DeepNesting(t *testing.T) {
	dir := t.TempDir()

	// Create directories for all paths referenced
	for _, sub := range []string{"parent", "parent/child", "parent/child/grandchild"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := `{
		"name": "ws",
		"projects": [
			{
				"name": "parent",
				"path": "parent",
				"language": "go",
				"services": [
					{
						"name": "child",
						"path": "parent/child",
						"language": "go",
						"services": [
							{"name": "grandchild", "path": "parent/child/grandchild", "language": "go"}
						]
					}
				]
			}
		]
	}`
	writeConfig(t, dir, cfg)

	loader := &Loader{RootDir: dir}
	_, err := loader.Load()
	if err == nil {
		t.Fatal("Load() expected error for deep nesting, got nil")
	}
	if !strings.Contains(err.Error(), "one level deep") {
		t.Errorf("expected error to mention 'one level deep', got: %v", err)
	}
}

func TestLoad_PathNotFound(t *testing.T) {
	dir := t.TempDir()

	// Create only one of the two project directories
	existingDir := filepath.Join(dir, "existing")
	if err := os.MkdirAll(existingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := `{
		"name": "ws",
		"projects": [
			{"name": "existing", "path": "existing", "language": "go"},
			{"name": "missing",  "path": "missing",  "language": "go"}
		]
	}`
	writeConfig(t, dir, cfg)

	loader := &Loader{RootDir: dir}
	ws, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() expected no error when path is missing (should warn+skip), got: %v", err)
	}

	// The missing project should be skipped; only the existing one remains
	if len(ws.Projects) != 1 {
		t.Fatalf("expected 1 project after skipping missing path, got %d", len(ws.Projects))
	}
	if ws.Projects[0].Name != "existing" {
		t.Errorf("expected remaining project to be %q, got %q", "existing", ws.Projects[0].Name)
	}
}

// ---- Detect tests ----

func TestDetect_GoProject(t *testing.T) {
	dir := t.TempDir()

	// Create a go.mod file
	goMod := "module example.com/myapp\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := &Loader{RootDir: dir}
	ws, err := loader.Detect()
	if err != nil {
		t.Fatalf("Detect() returned unexpected error: %v", err)
	}

	if len(ws.Projects) == 0 {
		t.Fatal("expected at least one project detected")
	}

	found := false
	for _, p := range ws.Projects {
		if p.Language == "go" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a Go project to be detected, got: %+v", ws.Projects)
	}
}

func TestDetect_TypeScriptProject(t *testing.T) {
	dir := t.TempDir()

	// package.json + tsconfig.json → TypeScript
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := &Loader{RootDir: dir}
	ws, err := loader.Detect()
	if err != nil {
		t.Fatalf("Detect() returned unexpected error: %v", err)
	}

	found := false
	for _, p := range ws.Projects {
		if p.Language == "typescript" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a TypeScript project to be detected, got: %+v", ws.Projects)
	}
}

func TestDetect_JavaScriptProject(t *testing.T) {
	dir := t.TempDir()

	// package.json only (no tsconfig.json) → JavaScript
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"app"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := &Loader{RootDir: dir}
	ws, err := loader.Detect()
	if err != nil {
		t.Fatalf("Detect() returned unexpected error: %v", err)
	}

	found := false
	for _, p := range ws.Projects {
		if p.Language == "javascript" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a JavaScript project to be detected, got: %+v", ws.Projects)
	}
}

func TestDetect_CSharpProject(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "MyApp.csproj"), []byte(`<Project/>`), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := &Loader{RootDir: dir}
	ws, err := loader.Detect()
	if err != nil {
		t.Fatalf("Detect() returned unexpected error: %v", err)
	}

	found := false
	for _, p := range ws.Projects {
		if p.Language == "csharp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a C# project to be detected, got: %+v", ws.Projects)
	}
}

func TestDetect_PythonProject(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(`[tool.poetry]\nname = "myapp"\n`), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := &Loader{RootDir: dir}
	ws, err := loader.Detect()
	if err != nil {
		t.Fatalf("Detect() returned unexpected error: %v", err)
	}

	found := false
	for _, p := range ws.Projects {
		if p.Language == "python" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a Python project to be detected, got: %+v", ws.Projects)
	}
}

func TestDetect_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	loader := &Loader{RootDir: dir}
	ws, err := loader.Detect()
	if err != nil {
		t.Fatalf("Detect() returned unexpected error: %v", err)
	}

	if len(ws.Projects) != 0 {
		t.Errorf("expected 0 projects in empty directory, got %d: %+v", len(ws.Projects), ws.Projects)
	}
}

// ---- Save tests ----

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Create the project directory so Load can validate it
	projDir := filepath.Join(dir, "svc")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ws := &Workspace{
		Name: "testworkspace",
		Projects: []Project{
			{
				Name:     "svc",
				Path:     "svc",
				Language: "go",
				Exclude:  []string{"vendor"},
			},
		},
	}

	loader := &Loader{RootDir: dir}
	if err := loader.Save(ws); err != nil {
		t.Fatalf("Save() returned unexpected error: %v", err)
	}

	// Verify the file exists and contains valid JSON
	data, err := os.ReadFile(loader.ConfigPath())
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}

	var decoded Workspace
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("saved config is not valid JSON: %v", err)
	}

	if decoded.Name != ws.Name {
		t.Errorf("expected name %q, got %q", ws.Name, decoded.Name)
	}
	if len(decoded.Projects) != len(ws.Projects) {
		t.Fatalf("expected %d projects, got %d", len(ws.Projects), len(decoded.Projects))
	}
	if decoded.Projects[0].Language != ws.Projects[0].Language {
		t.Errorf("expected language %q, got %q", ws.Projects[0].Language, decoded.Projects[0].Language)
	}
}

// ---- Property test (task 2.7) ----

// validLanguage generates a random supported language string.
func validLanguage(t *rapid.T) string {
	return rapid.SampledFrom(SupportedLanguages).Draw(t, "language")
}

// validName generates a non-empty alphanumeric name.
func validName(t *rapid.T, label string) string {
	return rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,19}`).Draw(t, label)
}

// TestProperty_SaveLoadRoundTrip verifies that for any valid Workspace,
// Save then Load produces an equivalent Workspace.
//
// **Validates: Requirements 1.3, 1.4, 1.5**
func TestProperty_SaveLoadRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()

		wsName := validName(rt, "wsName")

		// Generate 0–3 projects
		numProjects := rapid.IntRange(0, 3).Draw(rt, "numProjects")
		projects := make([]Project, 0, numProjects)

		for i := 0; i < numProjects; i++ {
			projName := validName(rt, "projName")
			lang := validLanguage(rt)

			// Use a unique subdirectory name to avoid collisions
			relPath := filepath.Join("proj", projName)
			absPath := filepath.Join(dir, relPath)
			if err := os.MkdirAll(absPath, 0o755); err != nil {
				rt.Fatal(err)
			}

			projects = append(projects, Project{
				Name:     projName,
				Path:     filepath.ToSlash(relPath),
				Language: lang,
			})
		}

		ws := &Workspace{
			Name:     wsName,
			Projects: projects,
		}

		loader := &Loader{RootDir: dir}

		// Save the workspace
		if err := loader.Save(ws); err != nil {
			rt.Fatalf("Save() failed: %v", err)
		}

		// Reset RootDir so Load re-discovers it from the file
		loader2 := &Loader{RootDir: dir}
		loaded, err := loader2.Load()
		if err != nil {
			rt.Fatalf("Load() failed after Save(): %v", err)
		}

		// Verify equivalence
		if loaded.Name != ws.Name {
			rt.Fatalf("workspace name mismatch: saved %q, loaded %q", ws.Name, loaded.Name)
		}
		if len(loaded.Projects) != len(ws.Projects) {
			rt.Fatalf("project count mismatch: saved %d, loaded %d", len(ws.Projects), len(loaded.Projects))
		}
		for i, orig := range ws.Projects {
			got := loaded.Projects[i]
			if got.Name != orig.Name {
				rt.Fatalf("project[%d] name mismatch: saved %q, loaded %q", i, orig.Name, got.Name)
			}
			if got.Language != orig.Language {
				rt.Fatalf("project[%d] language mismatch: saved %q, loaded %q", i, orig.Language, got.Language)
			}
			if got.Path != orig.Path {
				rt.Fatalf("project[%d] path mismatch: saved %q, loaded %q", i, orig.Path, got.Path)
			}
		}
	})
}
