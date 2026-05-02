-- Projects: workspace project registry
CREATE TABLE IF NOT EXISTS projects (
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL,
    path     TEXT NOT NULL,
    language TEXT NOT NULL
);

-- Symbols: all named code entities
CREATE TABLE IF NOT EXISTS symbols (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL,
    file        TEXT NOT NULL,
    start_line  INTEGER NOT NULL,
    end_line    INTEGER NOT NULL,
    signature   TEXT,
    doc_comment TEXT,
    project_id  TEXT NOT NULL
);

-- Full-text search index over symbol names, signatures, and doc comments
-- Using a standalone FTS table (not a content table) for simplicity and reliability
CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5(
    id UNINDEXED,
    name,
    signature,
    doc_comment
);

-- Edges: directed relationships between symbols
CREATE TABLE IF NOT EXISTS edges (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    from_id  TEXT NOT NULL,
    to_id    TEXT NOT NULL,
    kind     TEXT NOT NULL,
    file     TEXT NOT NULL,
    line     INTEGER NOT NULL
);

-- File tracking for incremental updates
CREATE TABLE IF NOT EXISTS file_index (
    path       TEXT PRIMARY KEY,
    hash       TEXT NOT NULL,
    mod_time   DATETIME NOT NULL,
    project_id TEXT NOT NULL DEFAULT ''
);

-- Indexes for common query patterns
CREATE INDEX IF NOT EXISTS idx_symbols_name    ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_kind    ON symbols(kind);
CREATE INDEX IF NOT EXISTS idx_symbols_file    ON symbols(file);
CREATE INDEX IF NOT EXISTS idx_symbols_project ON symbols(project_id);
CREATE INDEX IF NOT EXISTS idx_edges_from      ON edges(from_id);
CREATE INDEX IF NOT EXISTS idx_edges_to        ON edges(to_id);
CREATE INDEX IF NOT EXISTS idx_edges_kind      ON edges(kind);
