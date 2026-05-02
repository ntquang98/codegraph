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

**Supported language IDs:** `go`, `typescript`, `javascript`, `python`, `csharp`, `auto`

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

## License

MIT
