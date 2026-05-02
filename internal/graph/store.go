package graph

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"time"

	"github.com/codegraph-cli/codegraph/internal/parse"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

// Store is the SQLite-backed persistence layer for the code graph.
type Store struct {
	db *sql.DB
}

// SearchQuery holds parameters for filtering symbols.
type SearchQuery struct {
	Name      string
	Kind      parse.SymbolKind
	ProjectID string
	File      string
	Limit     int
	Offset    int
}

// ProjectStats holds aggregate statistics for a single project.
type ProjectStats struct {
	ProjectID   string
	SymbolCount int
	EdgeCount   int
	FileCount   int
}

// Open opens (or creates) the SQLite database at path, enables WAL mode and
// foreign key enforcement, and returns a ready-to-use Store.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("graph: open db: %w", err)
	}

	// Enable WAL mode for concurrent read/write access.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("graph: enable WAL: %w", err)
	}

	// Enable foreign key enforcement.
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("graph: enable foreign keys: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Migrate applies the embedded SQL schema, creating all tables and indexes if
// they do not already exist. It is safe to call multiple times (idempotent).
func (s *Store) Migrate() error {
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return fmt.Errorf("graph: read schema: %w", err)
	}

	// Split on semicolons and execute each statement individually because
	// database/sql does not support multi-statement Exec on all drivers.
	stmts := strings.Split(string(schema), ";")
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("graph: migrate: %w (stmt: %s)", err, stmt)
		}
	}
	return nil
}

// UpsertSymbols inserts or replaces all symbols in a single transaction and
// keeps the FTS index in sync atomically.
func (s *Store) UpsertSymbols(symbols []parse.Symbol) error {
	if len(symbols) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("graph: upsert symbols begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	symStmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO symbols
			(id, name, kind, file, start_line, end_line, signature, doc_comment, project_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("graph: prepare upsert symbol: %w", err)
	}
	defer symStmt.Close()

	// For the FTS table we delete the old entry (if any) then insert the new one.
	ftsDelStmt, err := tx.Prepare(`DELETE FROM symbols_fts WHERE id = ?`)
	if err != nil {
		return fmt.Errorf("graph: prepare fts delete: %w", err)
	}
	defer ftsDelStmt.Close()

	ftsInsStmt, err := tx.Prepare(`
		INSERT INTO symbols_fts (id, name, signature, doc_comment)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("graph: prepare fts insert: %w", err)
	}
	defer ftsInsStmt.Close()

	for _, sym := range symbols {
		if _, err := symStmt.Exec(
			sym.ID, sym.Name, string(sym.Kind), sym.File,
			sym.StartLine, sym.EndLine, sym.Signature, sym.DocComment, sym.ProjectID,
		); err != nil {
			return fmt.Errorf("graph: upsert symbol %s: %w", sym.ID, err)
		}

		// Keep FTS in sync: remove stale entry, insert fresh one.
		if _, err := ftsDelStmt.Exec(sym.ID); err != nil {
			return fmt.Errorf("graph: fts delete %s: %w", sym.ID, err)
		}
		if _, err := ftsInsStmt.Exec(sym.ID, sym.Name, sym.Signature, sym.DocComment); err != nil {
			return fmt.Errorf("graph: fts insert %s: %w", sym.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graph: upsert symbols commit: %w", err)
	}
	return nil
}

// UpsertEdges inserts or replaces all edges in a single transaction.
func (s *Store) UpsertEdges(edges []parse.Edge) error {
	if len(edges) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("graph: upsert edges begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Edges use an auto-increment PK so we cannot do a true INSERT OR REPLACE
	// without knowing the rowid. Instead we delete matching (from_id, to_id, kind,
	// file, line) tuples first, then insert fresh rows.
	delStmt, err := tx.Prepare(`
		DELETE FROM edges WHERE from_id = ? AND to_id = ? AND kind = ? AND file = ? AND line = ?
	`)
	if err != nil {
		return fmt.Errorf("graph: prepare edge delete: %w", err)
	}
	defer delStmt.Close()

	insStmt, err := tx.Prepare(`
		INSERT INTO edges (from_id, to_id, kind, file, line)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("graph: prepare edge insert: %w", err)
	}
	defer insStmt.Close()

	for _, e := range edges {
		if _, err := delStmt.Exec(e.FromID, e.ToID, string(e.Kind), e.File, e.Line); err != nil {
			return fmt.Errorf("graph: delete edge: %w", err)
		}
		if _, err := insStmt.Exec(e.FromID, e.ToID, string(e.Kind), e.File, e.Line); err != nil {
			return fmt.Errorf("graph: insert edge: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graph: upsert edges commit: %w", err)
	}
	return nil
}

// DeleteByFile removes all symbols and edges associated with filePath in a
// single atomic transaction.
func (s *Store) DeleteByFile(filePath string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("graph: delete by file begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Remove FTS entries for symbols in this file before deleting the symbols.
	if _, err := tx.Exec(`
		DELETE FROM symbols_fts WHERE id IN (SELECT id FROM symbols WHERE file = ?)
	`, filePath); err != nil {
		return fmt.Errorf("graph: delete fts for file %s: %w", filePath, err)
	}

	// Delete edges that originate from or point to symbols in this file.
	if _, err := tx.Exec(`
		DELETE FROM edges WHERE from_id IN (SELECT id FROM symbols WHERE file = ?)
		   OR to_id   IN (SELECT id FROM symbols WHERE file = ?)
	`, filePath, filePath); err != nil {
		return fmt.Errorf("graph: delete edges for file %s: %w", filePath, err)
	}

	// Also delete edges whose file column matches (edges stored by file).
	if _, err := tx.Exec(`DELETE FROM edges WHERE file = ?`, filePath); err != nil {
		return fmt.Errorf("graph: delete file edges for file %s: %w", filePath, err)
	}

	// Delete the symbols themselves.
	if _, err := tx.Exec(`DELETE FROM symbols WHERE file = ?`, filePath); err != nil {
		return fmt.Errorf("graph: delete symbols for file %s: %w", filePath, err)
	}

	// Remove the file index entry.
	if _, err := tx.Exec(`DELETE FROM file_index WHERE path = ?`, filePath); err != nil {
		return fmt.Errorf("graph: delete file index for %s: %w", filePath, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graph: delete by file commit: %w", err)
	}
	return nil
}

// SetFileHash stores (or updates) the content hash and modification time for a
// tracked file.
func (s *Store) SetFileHash(filePath, hash string, modTime time.Time) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO file_index (path, hash, mod_time, project_id)
		VALUES (?, ?, ?, COALESCE((SELECT project_id FROM file_index WHERE path = ?), ''))
	`, filePath, hash, modTime.UTC().Format(time.RFC3339Nano), filePath)
	if err != nil {
		return fmt.Errorf("graph: set file hash %s: %w", filePath, err)
	}
	return nil
}

// GetFileHash returns the stored content hash and modification time for a
// tracked file. Returns ("", zero, nil) when the file is not tracked.
func (s *Store) GetFileHash(filePath string) (string, time.Time, error) {
	var hash, modTimeStr string
	err := s.db.QueryRow(
		`SELECT hash, mod_time FROM file_index WHERE path = ?`, filePath,
	).Scan(&hash, &modTimeStr)
	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("graph: get file hash %s: %w", filePath, err)
	}

	modTime, err := time.Parse(time.RFC3339Nano, modTimeStr)
	if err != nil {
		// Fallback: try without nanoseconds.
		modTime, err = time.Parse(time.RFC3339, modTimeStr)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("graph: parse mod_time for %s: %w", filePath, err)
		}
	}
	return hash, modTime, nil
}

// UpsertProject inserts or replaces a project record in the projects table.
func (s *Store) UpsertProject(id, name, path, language string) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO projects (id, name, path, language) VALUES (?, ?, ?, ?)`,
		id, name, path, language,
	)
	if err != nil {
		return fmt.Errorf("graph: upsert project %s: %w", id, err)
	}
	return nil
}

// GetAllTrackedFiles returns all file paths currently in the file_index table.
func (s *Store) GetAllTrackedFiles() ([]string, error) {
	rows, err := s.db.Query(`SELECT path FROM file_index`)
	if err != nil {
		return nil, fmt.Errorf("graph: get all tracked files: %w", err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("graph: scan tracked file: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// SearchSymbols returns symbols matching the given query, filtered by name,
// kind, project, and file, with LIMIT/OFFSET pagination.
func (s *Store) SearchSymbols(query SearchQuery) ([]parse.Symbol, error) {
	var conditions []string
	var args []interface{}

	if query.Name != "" {
		conditions = append(conditions, "name LIKE ?")
		args = append(args, "%"+query.Name+"%")
	}
	if query.Kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, string(query.Kind))
	}
	if query.ProjectID != "" {
		conditions = append(conditions, "project_id = ?")
		args = append(args, query.ProjectID)
	}
	if query.File != "" {
		conditions = append(conditions, "file = ?")
		args = append(args, query.File)
	}

	q := `SELECT id, name, kind, file, start_line, end_line, signature, doc_comment, project_id FROM symbols`
	if len(conditions) > 0 {
		q += " WHERE " + strings.Join(conditions, " AND ")
	}
	q += " ORDER BY name"

	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	q += " LIMIT ? OFFSET ?"
	args = append(args, limit, query.Offset)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("graph: search symbols: %w", err)
	}
	defer rows.Close()

	return scanSymbols(rows)
}

// GetSymbol returns the symbol with the given ID, or nil if not found.
func (s *Store) GetSymbol(id string) (*parse.Symbol, error) {
	row := s.db.QueryRow(`
		SELECT id, name, kind, file, start_line, end_line, signature, doc_comment, project_id
		FROM symbols WHERE id = ?
	`, id)

	var sym parse.Symbol
	var sig, doc sql.NullString
	err := row.Scan(
		&sym.ID, &sym.Name, &sym.Kind, &sym.File,
		&sym.StartLine, &sym.EndLine, &sig, &doc, &sym.ProjectID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("graph: get symbol %s: %w", id, err)
	}
	sym.Signature = sig.String
	sym.DocComment = doc.String
	return &sym, nil
}

// GetCallers returns all symbols that have a directed `calls` path to
// symbolID within the given depth, excluding symbolID itself.
func (s *Store) GetCallers(symbolID string, depth int) ([]parse.Symbol, error) {
	const q = `
WITH RECURSIVE callers(id, depth) AS (
    SELECT e.from_id, 1
    FROM edges e
    WHERE e.to_id = ? AND e.kind = 'calls'
    UNION ALL
    SELECT e.from_id, c.depth + 1
    FROM edges e
    JOIN callers c ON e.to_id = c.id
    WHERE c.depth < ?
      AND e.kind = 'calls'
)
SELECT DISTINCT s.id, s.name, s.kind, s.file, s.start_line, s.end_line, s.signature, s.doc_comment, s.project_id
FROM symbols s
JOIN callers c ON s.id = c.id
WHERE s.id != ?
`
	rows, err := s.db.Query(q, symbolID, depth, symbolID)
	if err != nil {
		return nil, fmt.Errorf("graph: get callers %s: %w", symbolID, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// GetCallees returns all symbols that symbolID has a directed `calls` path to
// within the given depth, excluding symbolID itself.
func (s *Store) GetCallees(symbolID string, depth int) ([]parse.Symbol, error) {
	const q = `
WITH RECURSIVE callees(id, depth) AS (
    SELECT e.to_id, 1
    FROM edges e
    WHERE e.from_id = ? AND e.kind = 'calls'
    UNION ALL
    SELECT e.to_id, c.depth + 1
    FROM edges e
    JOIN callees c ON e.from_id = c.id
    WHERE c.depth < ?
      AND e.kind = 'calls'
)
SELECT DISTINCT s.id, s.name, s.kind, s.file, s.start_line, s.end_line, s.signature, s.doc_comment, s.project_id
FROM symbols s
JOIN callees c ON s.id = c.id
WHERE s.id != ?
`
	rows, err := s.db.Query(q, symbolID, depth, symbolID)
	if err != nil {
		return nil, fmt.Errorf("graph: get callees %s: %w", symbolID, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// GetDependencies returns all symbols reachable from symbolID via `imports`
// edges within the given depth.
func (s *Store) GetDependencies(symbolID string, depth int) ([]parse.Symbol, error) {
	const q = `
WITH RECURSIVE deps(id, depth) AS (
    SELECT e.to_id, 1
    FROM edges e
    WHERE e.from_id = ? AND e.kind = 'imports'
    UNION ALL
    SELECT e.to_id, d.depth + 1
    FROM edges e
    JOIN deps d ON e.from_id = d.id
    WHERE d.depth < ?
      AND e.kind = 'imports'
)
SELECT DISTINCT s.id, s.name, s.kind, s.file, s.start_line, s.end_line, s.signature, s.doc_comment, s.project_id
FROM symbols s
JOIN deps d ON s.id = d.id
WHERE s.id != ?
`
	rows, err := s.db.Query(q, symbolID, depth, symbolID)
	if err != nil {
		return nil, fmt.Errorf("graph: get dependencies %s: %w", symbolID, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// GetDependents returns all symbols that import symbolID within the given depth.
func (s *Store) GetDependents(symbolID string, depth int) ([]parse.Symbol, error) {
	const q = `
WITH RECURSIVE dependents(id, depth) AS (
    SELECT e.from_id, 1
    FROM edges e
    WHERE e.to_id = ? AND e.kind = 'imports'
    UNION ALL
    SELECT e.from_id, d.depth + 1
    FROM edges e
    JOIN dependents d ON e.to_id = d.id
    WHERE d.depth < ?
      AND e.kind = 'imports'
)
SELECT DISTINCT s.id, s.name, s.kind, s.file, s.start_line, s.end_line, s.signature, s.doc_comment, s.project_id
FROM symbols s
JOIN dependents d ON s.id = d.id
WHERE s.id != ?
`
	rows, err := s.db.Query(q, symbolID, depth, symbolID)
	if err != nil {
		return nil, fmt.Errorf("graph: get dependents %s: %w", symbolID, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// GetImplementors returns all symbols connected to interfaceID via `implements`
// edges.
func (s *Store) GetImplementors(interfaceID string) ([]parse.Symbol, error) {
	const q = `
SELECT DISTINCT s.id, s.name, s.kind, s.file, s.start_line, s.end_line, s.signature, s.doc_comment, s.project_id
FROM symbols s
JOIN edges e ON s.id = e.from_id
WHERE e.to_id = ? AND e.kind = 'implements'
`
	rows, err := s.db.Query(q, interfaceID)
	if err != nil {
		return nil, fmt.Errorf("graph: get implementors %s: %w", interfaceID, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// FullTextSearch queries the FTS5 index for symbols whose name, signature, or
// doc comment matches query, returning up to limit results.
func (s *Store) FullTextSearch(query string, limit int) ([]parse.Symbol, error) {
	if limit <= 0 {
		limit = 20
	}

	// Use the FTS match operator. We join back to symbols to get full rows.
	const q = `
SELECT s.id, s.name, s.kind, s.file, s.start_line, s.end_line, s.signature, s.doc_comment, s.project_id
FROM symbols s
JOIN symbols_fts f ON s.id = f.id
WHERE symbols_fts MATCH ?
LIMIT ?
`
	rows, err := s.db.Query(q, query, limit)
	if err != nil {
		return nil, fmt.Errorf("graph: full text search %q: %w", query, err)
	}
	defer rows.Close()
	return scanSymbols(rows)
}

// GetProjectStats returns aggregate symbol, edge, and file counts per project.
func (s *Store) GetProjectStats() ([]ProjectStats, error) {
	const q = `
SELECT
    p.id AS project_id,
    COUNT(DISTINCT s.id)   AS symbol_count,
    COUNT(DISTINCT e.id)   AS edge_count,
    COUNT(DISTINCT fi.path) AS file_count
FROM projects p
LEFT JOIN symbols s  ON s.project_id  = p.id
LEFT JOIN edges e    ON e.from_id IN (SELECT id FROM symbols WHERE project_id = p.id)
LEFT JOIN file_index fi ON fi.project_id = p.id
GROUP BY p.id
`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("graph: get project stats: %w", err)
	}
	defer rows.Close()

	var stats []ProjectStats
	for rows.Next() {
		var ps ProjectStats
		if err := rows.Scan(&ps.ProjectID, &ps.SymbolCount, &ps.EdgeCount, &ps.FileCount); err != nil {
			return nil, fmt.Errorf("graph: scan project stats: %w", err)
		}
		stats = append(stats, ps)
	}
	return stats, rows.Err()
}

// scanSymbols reads all rows from a *sql.Rows into a []parse.Symbol slice.
// It handles nullable signature and doc_comment columns.
func scanSymbols(rows *sql.Rows) ([]parse.Symbol, error) {
	var symbols []parse.Symbol
	for rows.Next() {
		var sym parse.Symbol
		var sig, doc sql.NullString
		if err := rows.Scan(
			&sym.ID, &sym.Name, &sym.Kind, &sym.File,
			&sym.StartLine, &sym.EndLine, &sig, &doc, &sym.ProjectID,
		); err != nil {
			return nil, fmt.Errorf("graph: scan symbol: %w", err)
		}
		sym.Signature = sig.String
		sym.DocComment = doc.String
		symbols = append(symbols, sym)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: rows error: %w", err)
	}
	return symbols, nil
}
