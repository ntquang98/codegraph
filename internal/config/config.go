package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// SupportedLanguages lists all valid language IDs for a project's language field.
var SupportedLanguages = []string{
	"go",
	"typescript",
	"javascript",
	"python",
	"csharp",
	"rust",
	"zig",
	"vue",
	"auto",
}

// Workspace represents the top-level structure of a .codegraph.json file.
type Workspace struct {
	Name     string    `json:"name"`
	Projects []Project `json:"projects"`
}

// Project represents a named sub-directory within the Workspace with a specific
// language and optional exclude patterns. Services are nested Projects used for
// monorepo sub-packages (one level deep only).
type Project struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`              // relative to workspace root
	Language string    `json:"language"`           // "go", "typescript", "javascript", "python", "csharp", "vue", or "auto"
	Exclude  []string  `json:"exclude,omitempty"`  // glob patterns to skip
	Services []Project `json:"services,omitempty"` // nested projects for monorepos (one level deep only)
}

// Loader reads, validates, and writes the .codegraph.json workspace config file.
type Loader struct {
	RootDir string
}

const configFileName = ".codegraph.json"

// Load locates .codegraph.json by walking up from CWD (or l.RootDir if set),
// unmarshals the JSON, and validates all fields.
func (l *Loader) Load() (*Workspace, error) {
	startDir := l.RootDir
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	// Walk up the directory tree looking for .codegraph.json
	configPath, err := findConfigFile(startDir)
	if err != nil {
		return nil, err
	}

	// Set RootDir to the directory containing the config file
	l.RootDir = filepath.Dir(configPath)

	// Read the config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", configPath, err)
	}

	// Unmarshal JSON
	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("invalid JSON in %s: %w", configPath, err)
	}

	// Validate the workspace
	if err := l.validate(&ws); err != nil {
		return nil, err
	}

	return &ws, nil
}

// findConfigFile walks up the directory tree from startDir looking for .codegraph.json.
// Returns the full path to the config file, or an error if not found.
func findConfigFile(startDir string) (string, error) {
	dir := startDir
	for {
		candidate := filepath.Join(dir, configFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding the file
			return "", fmt.Errorf("No %s found. Run 'codegraph install' first.", configFileName)
		}
		dir = parent
	}
}

// validate checks all fields of the workspace and its projects.
// Per Requirement 1.6, projects with non-existent paths are logged as warnings and skipped
// (the project is removed from the workspace's Projects slice).
func (l *Loader) validate(ws *Workspace) error {
	if ws.Name == "" {
		return fmt.Errorf("workspace name must be non-empty")
	}

	validProjects := ws.Projects[:0]
	for _, p := range ws.Projects {
		if err := l.validateProject(p, false); err != nil {
			// Check if this is a path-not-found error (warn and skip) vs a hard error
			if isPathNotFoundError(err) {
				log.Printf("warning: skipping project %q: %v", p.Name, err)
				continue
			}
			return err
		}
		validProjects = append(validProjects, p)
	}
	ws.Projects = validProjects

	return nil
}

// validateProject validates a single project. If nested is true, services within
// this project are not allowed (enforces one-level-deep nesting).
func (l *Loader) validateProject(p Project, nested bool) error {
	if p.Name == "" {
		return fmt.Errorf("project name must be non-empty")
	}

	if !isSupportedLanguage(p.Language) {
		return fmt.Errorf("project %q has unsupported language %q; must be one of %v", p.Name, p.Language, SupportedLanguages)
	}

	// Validate path exists on disk
	absPath := filepath.Join(l.RootDir, p.Path)
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return &pathNotFoundError{project: p.Name, path: absPath}
	}

	// Enforce one-level-deep nesting: services within services are not allowed
	if nested && len(p.Services) > 0 {
		return fmt.Errorf("project %q: services can only be nested one level deep (services within services are not allowed)", p.Name)
	}

	// Validate services (one level deep)
	for _, svc := range p.Services {
		if err := l.validateProject(svc, true); err != nil {
			if isPathNotFoundError(err) {
				log.Printf("warning: skipping service %q in project %q: %v", svc.Name, p.Name, err)
				continue
			}
			return err
		}
	}

	return nil
}

// pathNotFoundError is a sentinel error type for missing project paths.
type pathNotFoundError struct {
	project string
	path    string
}

func (e *pathNotFoundError) Error() string {
	return fmt.Sprintf("path %q does not exist on disk", e.path)
}

// isPathNotFoundError returns true if the error is a pathNotFoundError.
func isPathNotFoundError(err error) bool {
	_, ok := err.(*pathNotFoundError)
	return ok
}

// isSupportedLanguage returns true if lang is in SupportedLanguages.
func isSupportedLanguage(lang string) bool {
	for _, supported := range SupportedLanguages {
		if lang == supported {
			return true
		}
	}
	return false
}

// Save marshals the workspace to indented JSON and writes it to ConfigPath().
func (l *Loader) Save(ws *Workspace) error {
	data, err := json.MarshalIndent(ws, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workspace: %w", err)
	}

	configPath := l.ConfigPath()

	// Create parent directories if needed
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", configPath, err)
	}

	return nil
}

// Detect scans RootDir for recognizable project manifest files (go.mod,
// package.json, *.csproj, pyproject.toml) and builds a Workspace with
// detected projects and languages.
func (l *Loader) Detect() (*Workspace, error) {
	rootDir := l.RootDir
	if rootDir == "" {
		var err error
		rootDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current working directory: %w", err)
		}
	}

	ws := &Workspace{
		Name:     filepath.Base(rootDir),
		Projects: []Project{},
	}

	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories (e.g. .git, .kiro) but not the root itself
		if d.IsDir() && path != rootDir && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}

		// Skip common dependency/build directories
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "vendor", "dist", "build", "bin", "obj", "__pycache__", ".venv", "venv":
				return filepath.SkipDir
			}
		}

		if d.IsDir() {
			return nil
		}

		name := d.Name()
		dir := filepath.Dir(path)

		var project *Project

		switch {
		case name == "go.mod":
			lang := "go"
			projName := goModuleName(path)
			if projName == "" {
				projName = filepath.Base(dir)
			}
			relPath, err := filepath.Rel(rootDir, dir)
			if err != nil {
				return err
			}
			project = &Project{
				Name:     projName,
				Path:     relPath,
				Language: lang,
			}

		case name == "package.json":
			lang := "javascript"
			// Check for tsconfig.json in the same directory
			if _, err := os.Stat(filepath.Join(dir, "tsconfig.json")); err == nil {
				lang = "typescript"
			}
			relPath, err := filepath.Rel(rootDir, dir)
			if err != nil {
				return err
			}
			project = &Project{
				Name:     filepath.Base(dir),
				Path:     relPath,
				Language: lang,
			}

		case name == "pyproject.toml" || name == "setup.py":
			relPath, err := filepath.Rel(rootDir, dir)
			if err != nil {
				return err
			}
			project = &Project{
				Name:     filepath.Base(dir),
				Path:     relPath,
				Language: "python",
			}

		case strings.HasSuffix(name, ".csproj"):
			relPath, err := filepath.Rel(rootDir, dir)
			if err != nil {
				return err
			}
			// Use the csproj filename (without extension) as the project name
			projName := strings.TrimSuffix(name, ".csproj")
			project = &Project{
				Name:     projName,
				Path:     relPath,
				Language: "csharp",
			}

		case name == "Cargo.toml":
			relPath, err := filepath.Rel(rootDir, dir)
			if err != nil {
				return err
			}
			project = &Project{
				Name:     filepath.Base(dir),
				Path:     relPath,
				Language: "rust",
			}

		case name == "build.zig":
			relPath, err := filepath.Rel(rootDir, dir)
			if err != nil {
				return err
			}
			project = &Project{
				Name:     filepath.Base(dir),
				Path:     relPath,
				Language: "zig",
			}
		}

		if project != nil {
			// Normalize path separator to forward slash for cross-platform consistency
			project.Path = filepath.ToSlash(project.Path)
			// Use "." for the root directory itself
			if project.Path == "" {
				project.Path = "."
			}
			ws.Projects = append(ws.Projects, *project)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to scan directory %s: %w", rootDir, err)
	}

	return ws, nil
}

// goModuleName reads the module name from a go.mod file.
// Returns an empty string if the file cannot be read or parsed.
func goModuleName(goModPath string) string {
	f, err := os.Open(goModPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				// Return just the last path component as the project name
				mod := parts[1]
				return filepath.Base(mod)
			}
		}
	}
	return ""
}

// ConfigPath returns the absolute path to .codegraph.json in RootDir.
func (l *Loader) ConfigPath() string {
	return filepath.Join(l.RootDir, configFileName)
}
