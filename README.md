# codegraph

A Go command-line tool that parses multi-language source code repositories using [tree-sitter](https://tree-sitter.github.io/tree-sitter/), builds a persistent code knowledge graph stored in SQLite, and exposes that graph through two interfaces:

- **MCP server** — a JSON-RPC 2.0 tool server for AI coding agents (Claude, Cursor, etc.)
- **Browser UI** — a force-directed graph visualization for human exploration

Supports Go, TypeScript, JavaScript, Python, and C# in monorepos and multi-project workspaces. Distributed as a **single self-contained binary** with no external runtime dependencies.

---

## Installation

### From source

Requires Go 1.22 or later.

```bash
git clone https://github.com/codegraph-cli/codegraph
cd codegraph
make build
# Binary is at ./bin/codegraph
```

Move the binary somewhere on your `PATH`:

```bash
mv ./bin/codegraph /usr/local/bin/codegraph   # Linux / macOS
# or add ./bin to your PATH on Windows
```

### Verify the install

```bash
codegraph --help
```

---

## Quick Start

### 1. Initialize the workspace

Run this once in the root of your repository. `codegraph` will scan for project manifest files (`go.mod`, `package.json`, `*.csproj`, `pyproject.toml`) and write a `.codegraph.json` config file.

```bash
codegraph install
```

Review and edit `.codegraph.json` if needed — you can add `exclude` patterns, rename projects, or adjust language settings.

### 2. Build the code graph

Parse all source files and store the resulting symbols and edges in `.codegraph.db`:

```bash
codegraph build
# Build complete: 5,237 symbols, 16,435 edges across 847 files
```

### 3a. Start the MCP server (for AI agents)

```bash
codegraph agent mcp
# MCP server listening on http://127.0.0.1:3333/mcp
```

Point your AI agent at `http://127.0.0.1:3333/mcp`. The server exposes nine tools for querying the code graph (see [MCP Tools](#mcp-tools) below).

### 3b. Start the visual UI (for humans)

```bash
codegraph ui
# Graph UI available at http://127.0.0.1:8080
```

The default browser opens automatically. Use `--no-open` to suppress this.

### 4. Keep the graph up to date

After making code changes, run an incremental update instead of a full rebuild:

```bash
codegraph update
# Update complete: +1 added, ~3 modified, -0 deleted files
#   Symbols delta: +47, Edges delta: +89
```

### 5. Clean up

```bash
codegraph clean
# Removes .codegraph.json and .codegraph.db
```

---

## Workspace Configuration (`.codegraph.json`)

`codegraph install` generates this file automatically. You can edit it manually.

```json
{
  "name": "my-monorepo",
  "projects": [
    {
      "name": "backend",
      "path": "./backend",
      "language": "go",
      "exclude": ["vendor", "testdata"]
    },
    {
      "name": "frontend",
      "path": "./frontend",
      "language": "typescript",
      "exclude": ["node_modules", "dist", "*.test.ts"],
      "services": [
        { "name": "app",    "path": "./frontend/app",    "language": "typescript" },
        { "name": "shared", "path": "./frontend/shared", "language": "typescript" }
      ]
    }
  ]
}
```

**Fields:**

| Field | Description |
|---|---|
| `name` | Workspace name (required) |
| `projects[].name` | Project name (required) |
| `projects[].path` | Path relative to workspace root (required, must exist) |
| `projects[].language` | Language ID or `"auto"` to detect by extension |
| `projects[].exclude` | Glob patterns to skip (gitignore-style) |
| `projects[].services` | Nested sub-projects for monorepos (one level deep only) |

**Supported language IDs:** `go`, `typescript`, `javascript`, `python`, `csharp`, `rust`, `zig`, `auto`

---

## CLI Commands

### `codegraph install`

Scan the current directory for project manifests and write `.codegraph.json`.

```
codegraph install [--workspace <dir>] [--output text|json]
```

### `codegraph build`

Parse all source files and build the full code graph from scratch.

```
codegraph build [--workspace <dir>] [--output text|json]
```

Files whose content hash is unchanged since the last build are skipped. Each file's symbols and edges are written atomically.

### `codegraph update`

Incrementally update the graph after code changes. Only added, modified, and deleted files are processed.

```
codegraph update [--workspace <dir>] [--output text|json]
```

The resulting graph state is equivalent to a full `build` on the current workspace.

### `codegraph agent mcp`

Start the MCP (Model Context Protocol) server for AI coding agents.

```
codegraph agent mcp [--host 127.0.0.1] [--port 3333]
```

| Flag | Default | Description |
|---|---|---|
| `--host` | `127.0.0.1` | Host to bind to (a warning is printed for non-localhost values) |
| `--port` | `3333` | Port to listen on |

### `codegraph ui`

Start the browser-based graph visualization UI.

```
codegraph ui [--host 127.0.0.1] [--port 8080] [--no-open]
```

| Flag | Default | Description |
|---|---|---|
| `--host` | `127.0.0.1` | Host to bind to (a warning is printed for non-localhost values) |
| `--port` | `8080` | Port to listen on |
| `--no-open` | false | Do not open the browser automatically |

### `codegraph clean`

Remove `.codegraph.json` and `.codegraph.db` from the workspace root.

```
codegraph clean [--workspace <dir>] [--output text|json]
```

### Global Flags

| Flag | Description |
|---|---|
| `--output text\|json` | Format output as human-readable text (default) or JSON |
| `--workspace <dir>` | Override the workspace root directory |

---

## MCP Tools

When the MCP server is running, AI agents can call these nine tools via JSON-RPC 2.0 at `POST /mcp`.

### `codegraph/search`

Search for symbols by name, kind, project, or file.

| Argument | Type | Required | Description |
|---|---|---|---|
| `name` | string | — | Symbol name (substring match) |
| `kind` | string | — | `function`, `method`, `type`, `interface`, `variable`, `module`, `class` |
| `projectId` | string | — | Restrict to a specific project |
| `file` | string | — | Restrict to a specific file path |
| `limit` | number | — | Max results (default 20) |
| `offset` | number | — | Pagination offset (default 0) |

### `codegraph/callers`

Find all symbols that (transitively) call the given symbol.

| Argument | Type | Required | Description |
|---|---|---|---|
| `symbolId` | string | ✓ | Target symbol ID |
| `depth` | number | — | Max traversal depth, 1–10 (default 3) |

### `codegraph/callees`

Find all symbols that the given symbol (transitively) calls.

| Argument | Type | Required | Description |
|---|---|---|---|
| `symbolId` | string | ✓ | Source symbol ID |
| `depth` | number | — | Max traversal depth, 1–10 (default 3) |

### `codegraph/dependencies`

Find all symbols reachable from the given symbol via import edges.

| Argument | Type | Required | Description |
|---|---|---|---|
| `symbolId` | string | ✓ | Source symbol ID |
| `depth` | number | — | Max traversal depth, 1–10 (default 2) |

### `codegraph/dependents`

Find all symbols that import the given symbol.

| Argument | Type | Required | Description |
|---|---|---|---|
| `symbolId` | string | ✓ | Target symbol ID |
| `depth` | number | — | Max traversal depth, 1–10 (default 2) |

### `codegraph/implementors`

Find all symbols that implement the given interface.

| Argument | Type | Required | Description |
|---|---|---|---|
| `symbolId` | string | ✓ | Interface symbol ID |

### `codegraph/grep`

Full-text search across symbol names, signatures, and doc comments.

| Argument | Type | Required | Description |
|---|---|---|---|
| `query` | string | ✓ | Search query |
| `limit` | number | — | Max results, 1–100 (default 20) |

### `codegraph/symbol`

Get full details of a symbol by ID, including its immediate callers and callees.

| Argument | Type | Required | Description |
|---|---|---|---|
| `symbolId` | string | ✓ | Symbol ID |

### `codegraph/stats`

Get aggregate statistics (symbol count, edge count, file count) per project. No arguments required.

---

## Example MCP Tool Calls

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "codegraph/callers",
    "arguments": { "symbolId": "a3f2b1c4d5e6f7a8", "depth": 3 }
  }
}
```

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "codegraph/grep",
    "arguments": { "query": "handlePayment", "limit": 10 }
  }
}
```

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "codegraph/implementors",
    "arguments": { "symbolId": "b9c8d7e6f5a4b3c2" }
  }
}
```

---

## Development

### Build

```bash
make build          # produces ./bin/codegraph
```

### Test

```bash
make test           # all tests with race detector
make test-property  # property-based tests with 1000 iterations (override: RAPID_CHECKS=5000)
```

### Lint

```bash
make lint           # go vet + staticcheck (if installed)
# Install staticcheck: go install honnef.co/go/tools/cmd/staticcheck@latest
```

### CGo-free build

The binary uses `modernc.org/sqlite` — a pure-Go SQLite driver that requires no CGo or external shared libraries. The build is always run with `CGO_ENABLED=0`.

```bash
CGO_ENABLED=0 go build -o ./bin/codegraph .
```

---

## Security

- The MCP server and UI server bind to `127.0.0.1` by default. A warning is printed when `--host` is set to a non-localhost value.
- All file paths from `.codegraph.json` are validated to be within the workspace root before any files are read (path traversal protection).
- All SQL queries use parameterized statements — no string interpolation of user-provided values.
- The tool never executes source code it parses; parsing is read-only AST analysis.

---

## Adding a New Language

This section walks through every step required to add a new language extractor. Follow the checklist at the end to track your progress.

### 1. The `Extractor` interface

Every language extractor implements the `Extractor` interface defined in `internal/parse/registry.go`:

```go
type Extractor interface {
    Language()   string
    Extensions() []string
    Extract(path string, src []byte) ([]Symbol, []Edge, error)
}
```

**Contract:**

| Method | Contract |
|---|---|
| `Language()` | Returns the canonical language ID string (e.g. `"rust"`, `"zig"`). Must match the ID used in `.codegraph.json` and `SupportedLanguages`. |
| `Extensions()` | Returns the list of file extensions this extractor handles (e.g. `[]string{".rs"}`). Extensions must be lowercase and include the leading dot. |
| `Extract(path, src)` | Parses `src` (the full file content) and returns all `Symbol`s and `Edge`s found. `Symbol.File` must equal `path` for every returned symbol. Symbol IDs within the result set must be unique. Returns a non-nil error if the tree-sitter parser fails. |

### 2. Add the tree-sitter grammar dependency

Find the Go binding for your language's tree-sitter grammar on GitHub (search for `tree-sitter-<lang>` with a `binding.go`). Then add it to `go.mod`:

```bash
go get github.com/<owner>/tree-sitter-<lang>@latest
go mod tidy
```

> **Note:** Some grammars are sub-packages of the existing `github.com/smacker/go-tree-sitter` module (e.g. Rust). Check there first before adding a new module dependency.

### 3. Create the CGo extractor file

Create `internal/parse/extractor_<lang>.go`. The file must start with the CGo build tag and follow the same structure as the existing extractors (e.g. `extractor_rust.go`, `extractor_go.go`):

```go
//go:build cgo

package parse

import (
    "github.com/<owner>/tree-sitter-<lang>"
    sitter "github.com/smacker/go-tree-sitter"
)

type MyLangExtractor struct{}

func (e *MyLangExtractor) Language() string        { return "mylang" }
func (e *MyLangExtractor) Extensions() []string    { return []string{".ext"} }

func (e *MyLangExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
    parser := sitter.NewParser()
    parser.SetLanguage(mylang.GetLanguage())
    tree := parser.Parse(nil, src)
    if tree == nil {
        return nil, nil, fmt.Errorf("tree-sitter failed to parse %s", path)
    }
    // Walk tree.RootNode() and produce Symbols and Edges ...
}
```

Use `GenerateSymbolID(projectID, path, name, kind, workspaceRoot)` to produce stable symbol IDs. Qualify method names as `TypeName.method_name` to ensure uniqueness within a file.

### 4. Add the no-CGo stub

Open `internal/parse/extractors_nocgo.go` and add a stub for your extractor under the `//go:build !cgo` tag. Follow the exact pattern of the existing stubs:

```go
type MyLangExtractor struct{}

func (e *MyLangExtractor) Language() string     { return "mylang" }
func (e *MyLangExtractor) Extensions() []string { return []string{".ext"} }
func (e *MyLangExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
    return nil, nil, fmt.Errorf("mylang extractor requires CGO_ENABLED=1; rebuild with CGO_ENABLED=1 to enable it")
}
```

This ensures the binary compiles with `CGO_ENABLED=0` and produces a clear error message instead of a build failure.

### 5. Register the extractor at startup

Find where the existing extractors are registered (in `cmd/build.go`, look for `registry.Register`). Add your extractor in the same place:

```go
registry.Register(&parse.MyLangExtractor{})
```

After registration, `registry.SupportedExtensions()` will include your new extension and files will be routed to your extractor automatically during `codegraph build` and `codegraph update`.

### 6. Add the language ID to the config loader

Open `internal/config/config.go` and add your language ID to the `SupportedLanguages` slice:

```go
var SupportedLanguages = []string{
    "go",
    // ... existing entries ...
    "mylang",  // add here
    "auto",
}
```

### 7. Add auto-detection logic

In the same file, add a case to the `switch` block inside `Loader.Detect()` that matches your language's project manifest file (e.g. `Cargo.toml` for Rust, `build.zig` for Zig):

```go
case name == "mylang.toml":
    relPath, err := filepath.Rel(rootDir, dir)
    if err != nil {
        return err
    }
    project = &Project{
        Name:     filepath.Base(dir),
        Path:     relPath,
        Language: "mylang",
    }
```

After this change, `codegraph install` will automatically detect projects that contain your manifest file and set the correct language.

### 8. Create test fixtures and write unit tests

Create at least two source files in `testdata/<lang>-sample/` that exercise the full range of constructs your extractor handles (functions, types, imports, method definitions, call sites, etc.).

Then add unit tests in `internal/parse/extractor_<lang>_test.go`:

```go
//go:build cgo

func TestMyLangExtractor_BasicSymbols(t *testing.T) {
    e := &MyLangExtractor{}
    src := []byte(`/* small representative snippet */`)
    syms, edges, err := e.Extract("test.ext", src)
    require.NoError(t, err)
    // assert expected symbol names, kinds, and edge types
}

func TestMyLangExtractor_Fixtures(t *testing.T) {
    e := &MyLangExtractor{}
    for _, f := range []string{"testdata/mylang-sample/models.ext", "testdata/mylang-sample/service.ext"} {
        src, _ := os.ReadFile(f)
        syms, edges, err := e.Extract(f, src)
        require.NoError(t, err)
        assert.NotEmpty(t, syms)
        assert.NotEmpty(t, edges)
    }
}
```

Verify that all `Symbol.File` fields equal the input path and that all Symbol IDs within a result set are unique.

### Checklist

- [ ] Add the tree-sitter grammar module to `go.mod` (`go get`) and run `go mod tidy`
- [ ] Create `internal/parse/extractor_<lang>.go` with `//go:build cgo` tag implementing the `Extractor` interface
- [ ] Add a no-CGo stub to `internal/parse/extractors_nocgo.go` with `//go:build !cgo` tag
- [ ] Register the extractor with `registry.Register(&parse.MyLangExtractor{})` at startup
- [ ] Add the language ID to `SupportedLanguages` in `internal/config/config.go`
- [ ] Add manifest file detection to `Loader.Detect()` in `internal/config/config.go`
- [ ] Create `testdata/<lang>-sample/` with at least two representative source files
- [ ] Write unit tests in `internal/parse/extractor_<lang>_test.go` verifying symbols, edges, `Symbol.File`, and ID uniqueness
- [ ] Verify `CGO_ENABLED=1 go test ./...` passes
- [ ] Verify `CGO_ENABLED=0 go build ./...` compiles without errors

---

## License

MIT
