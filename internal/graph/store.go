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

	// Limit to a single connection so all PRAGMA settings are applied to the
	// same connection that executes queries (SQLite is not safe for concurrent
	// writes anyway).
	db.SetMaxOpenConns(1)

	pragmas := []string{
		// WAL mode: readers don't block writers and vice-versa.
		"PRAGMA journal_mode=WAL",
		// NORMAL sync is safe with WAL and much faster than FULL (the default).
		"PRAGMA synchronous=NORMAL",
		// 64 MB page cache kept in memory.
		"PRAGMA cache_size=-65536",
		// Store temp tables / indexes in memory instead of on disk.
		"PRAGMA temp_store=MEMORY",
		// Memory-map up to 256 MB of the database file for faster reads.
		"PRAGMA mmap_size=268435456",
		// Foreign key enforcement.
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("graph: %s: %w", p, err)
		}
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
//
// Instead of per-row prepared-statement loops, it builds bulk INSERT statements
// with up to sqliteMaxVars/9 rows per chunk (9 placeholders per symbol row) to
// stay within SQLite's SQLITE_MAX_VARIABLE_NUMBER limit.
func (s *Store) UpsertSymbols(symbols []parse.Symbol) error {
	if len(symbols) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("graph: upsert symbols begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := upsertSymbolsTx(tx, symbols); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graph: upsert symbols commit: %w", err)
	}
	return nil
}

// upsertSymbolsTx performs the bulk symbol + FTS upsert inside an existing
// transaction. It is also called by UpsertBatch so both share one transaction.
func upsertSymbolsTx(tx *sql.Tx, symbols []parse.Symbol) error {
	// SQLite's default SQLITE_MAX_VARIABLE_NUMBER is 999 (or 32766 on newer
	// builds). We use 999 as a safe lower bound.
	const maxVars = 999
	const colsPerSymbol = 9 // symbols table
	const colsPerFTS = 4    // fts table
	symChunk := maxVars / colsPerSymbol // 111 rows per chunk
	ftsChunk := maxVars / colsPerFTS    // 249 rows per chunk

	// Bulk upsert into symbols table.
	for i := 0; i < len(symbols); i += symChunk {
		end := i + symChunk
		if end > len(symbols) {
			end = len(symbols)
		}
		chunk := symbols[i:end]

		rows := make([]string, len(chunk))
		for j := range rows {
			rows[j] = "(?,?,?,?,?,?,?,?,?)"
		}
		q := "INSERT OR REPLACE INTO symbols (id, name, kind, file, start_line, end_line, signature, doc_comment, project_id) VALUES " + strings.Join(rows, ",")

		args := make([]interface{}, 0, len(chunk)*colsPerSymbol)
		for _, sym := range chunk {
			args = append(args, sym.ID, sym.Name, string(sym.Kind), sym.File,
				sym.StartLine, sym.EndLine, sym.Signature, sym.DocComment, sym.ProjectID)
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("graph: bulk upsert symbols: %w", err)
		}
	}

	// Collect IDs for bulk FTS delete.
	ids := make([]interface{}, len(symbols))
	idMarks := make([]string, len(symbols))
	for i, sym := range symbols {
		ids[i] = sym.ID
		idMarks[i] = "?"
	}

	// Bulk delete stale FTS entries (chunked to stay within variable limit).
	for i := 0; i < len(ids); i += maxVars {
		end := i + maxVars
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[i:end]
		marks := idMarks[i:end]
		if _, err := tx.Exec("DELETE FROM symbols_fts WHERE id IN ("+strings.Join(marks, ",")+`)`+``, chunk...); err != nil {
			return fmt.Errorf("graph: bulk fts delete: %w", err)
		}
	}

	// Bulk insert fresh FTS entries.
	for i := 0; i < len(symbols); i += ftsChunk {
		end := i + ftsChunk
		if end > len(symbols) {
			end = len(symbols)
		}
		chunk := symbols[i:end]

		rows := make([]string, len(chunk))
		for j := range rows {
			rows[j] = "(?,?,?,?)"
		}
		q := "INSERT INTO symbols_fts (id, name, signature, doc_comment) VALUES " + strings.Join(rows, ",")

		args := make([]interface{}, 0, len(chunk)*colsPerFTS)
		for _, sym := range chunk {
			args = append(args, sym.ID, sym.Name, sym.Signature, sym.DocComment)
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("graph: bulk fts insert: %w", err)
		}
	}

	return nil
}

// UpsertEdges inserts or replaces all edges in a single transaction.
//
// Uses bulk DELETE + INSERT with chunked placeholders to avoid per-row
// round-trips and stay within SQLite's variable limit.
func (s *Store) UpsertEdges(edges []parse.Edge) error {
	if len(edges) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("graph: upsert edges begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := upsertEdgesTx(tx, edges); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graph: upsert edges commit: %w", err)
	}
	return nil
}

// upsertEdgesTx performs the bulk edge upsert inside an existing transaction.
func upsertEdgesTx(tx *sql.Tx, edges []parse.Edge) error {
	const maxVars = 999
	const colsPerEdge = 5 // from_id, to_id, kind, file, line
	edgeChunk := maxVars / colsPerEdge // 199 rows per chunk

	// Delete matching edges first (edges use auto-increment PK so INSERT OR
	// REPLACE would create duplicates). We delete by the logical key tuple.
	for i := 0; i < len(edges); i += edgeChunk {
		end := i + edgeChunk
		if end > len(edges) {
			end = len(edges)
		}
		chunk := edges[i:end]

		rows := make([]string, len(chunk))
		for j := range rows {
			rows[j] = "(?,?,?,?,?)"
		}
		q := "DELETE FROM edges WHERE (from_id, to_id, kind, file, line) IN (" + strings.Join(rows, ",") + ")"
		args := make([]interface{}, 0, len(chunk)*colsPerEdge)
		for _, e := range chunk {
			args = append(args, e.FromID, e.ToID, string(e.Kind), e.File, e.Line)
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("graph: bulk delete edges: %w", err)
		}
	}

	// Bulk insert fresh edges.
	for i := 0; i < len(edges); i += edgeChunk {
		end := i + edgeChunk
		if end > len(edges) {
			end = len(edges)
		}
		chunk := edges[i:end]

		rows := make([]string, len(chunk))
		for j := range rows {
			rows[j] = "(?,?,?,?,?)"
		}
		q := "INSERT INTO edges (from_id, to_id, kind, file, line) VALUES " + strings.Join(rows, ",")
		args := make([]interface{}, 0, len(chunk)*colsPerEdge)
		for _, e := range chunk {
			args = append(args, e.FromID, e.ToID, string(e.Kind), e.File, e.Line)
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("graph: bulk insert edges: %w", err)
		}
	}

	return nil
}

// UpsertBatch writes symbols and edges in a single atomic transaction,
// avoiding the overhead of two separate commits per flush.
func (s *Store) UpsertBatch(symbols []parse.Symbol, edges []parse.Edge) error {
	if len(symbols) == 0 && len(edges) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("graph: upsert batch begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if len(symbols) > 0 {
		if err := upsertSymbolsTx(tx, symbols); err != nil {
			return err
		}
	}
	if len(edges) > 0 {
		if err := upsertEdgesTx(tx, edges); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("graph: upsert batch commit: %w", err)
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

// FileIndexEntry holds the stored hash and modification time for a tracked file.
type FileIndexEntry struct {
	Hash    string
	ModTime time.Time
}

// GetAllFileIndex returns a map of path → FileIndexEntry for every file
// currently tracked in the file_index table. Loading the entire index in one
// query avoids N individual round-trips during hash-change detection.
func (s *Store) GetAllFileIndex() (map[string]FileIndexEntry, error) {
	rows, err := s.db.Query(`SELECT path, hash, mod_time FROM file_index`)
	if err != nil {
		return nil, fmt.Errorf("graph: get all file index: %w", err)
	}
	defer rows.Close()

	index := make(map[string]FileIndexEntry)
	for rows.Next() {
		var path, hash, modTimeStr string
		if err := rows.Scan(&path, &hash, &modTimeStr); err != nil {
			return nil, fmt.Errorf("graph: scan file index entry: %w", err)
		}
		modTime, err := time.Parse(time.RFC3339Nano, modTimeStr)
		if err != nil {
			modTime, err = time.Parse(time.RFC3339, modTimeStr)
			if err != nil {
				return nil, fmt.Errorf("graph: parse mod_time for %s: %w", path, err)
			}
		}
		index[path] = FileIndexEntry{Hash: hash, ModTime: modTime}
	}
	return index, rows.Err()
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
