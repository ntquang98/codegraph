package graph

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/codegraph-cli/codegraph/internal/parse"
	"pgregory.net/rapid"
)

// openTestStore opens an in-memory SQLite store and runs Migrate.
// It registers a cleanup function to close the store when the test ends.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// insertProject inserts a minimal project row to satisfy the project_id
// foreign-key-like constraint on symbols.
func insertProject(t *testing.T, s *Store, id string) {
	t.Helper()
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO projects (id, name, path, language) VALUES (?, ?, ?, ?)`,
		id, id, "/"+id, "go",
	)
	if err != nil {
		t.Fatalf("insertProject %s: %v", id, err)
	}
}

// sortByID sorts a slice of symbols in-place by their ID field.
func sortByID(syms []parse.Symbol) {
	sort.Slice(syms, func(i, j int) bool { return syms[i].ID < syms[j].ID })
}

// symbolIDs returns a sorted slice of IDs from a symbol slice.
func symbolIDs(syms []parse.Symbol) []string {
	ids := make([]string, len(syms))
	for i, s := range syms {
		ids[i] = s.ID
	}
	sort.Strings(ids)
	return ids
}

// ─── 1. TestMigrateFreshDB ────────────────────────────────────────────────────

func TestMigrateFreshDB(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	// Verify expected tables exist in sqlite_master.
	expectedTables := []string{"symbols", "edges", "projects", "file_index"}
	for _, table := range expectedTables {
		var name string
		err := s.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found in sqlite_master: %v", table, err)
		}
	}
}

// ─── 2. TestMigrateIdempotent ─────────────────────────────────────────────────

func TestMigrateIdempotent(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.Migrate(); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
}

// ─── 3. TestUpsertSymbolsRoundTrip ────────────────────────────────────────────

func TestUpsertSymbolsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")

	sym1 := parse.Symbol{
		ID:         "sym-001",
		Name:       "FooBar",
		Kind:       parse.KindFunction,
		File:       "src/foo.go",
		StartLine:  10,
		EndLine:    20,
		Signature:  "func FooBar() error",
		DocComment: "FooBar does something.",
		ProjectID:  "proj1",
	}
	sym2 := parse.Symbol{
		ID:         "sym-002",
		Name:       "BazQux",
		Kind:       parse.KindMethod,
		File:       "src/baz.go",
		StartLine:  5,
		EndLine:    15,
		Signature:  "func (b *Baz) Qux(x int) string",
		DocComment: "BazQux does something else.",
		ProjectID:  "proj1",
	}

	if err := s.UpsertSymbols([]parse.Symbol{sym1, sym2}); err != nil {
		t.Fatalf("UpsertSymbols: %v", err)
	}

	// Verify GetSymbol round-trip for each symbol.
	for _, want := range []parse.Symbol{sym1, sym2} {
		got, err := s.GetSymbol(want.ID)
		if err != nil {
			t.Fatalf("GetSymbol(%s): %v", want.ID, err)
		}
		if got == nil {
			t.Fatalf("GetSymbol(%s): returned nil", want.ID)
		}
		if got.ID != want.ID {
			t.Errorf("ID: got %q, want %q", got.ID, want.ID)
		}
		if got.Name != want.Name {
			t.Errorf("Name: got %q, want %q", got.Name, want.Name)
		}
		if got.Kind != want.Kind {
			t.Errorf("Kind: got %q, want %q", got.Kind, want.Kind)
		}
		if got.File != want.File {
			t.Errorf("File: got %q, want %q", got.File, want.File)
		}
		if got.StartLine != want.StartLine {
			t.Errorf("StartLine: got %d, want %d", got.StartLine, want.StartLine)
		}
		if got.EndLine != want.EndLine {
			t.Errorf("EndLine: got %d, want %d", got.EndLine, want.EndLine)
		}
		if got.Signature != want.Signature {
			t.Errorf("Signature: got %q, want %q", got.Signature, want.Signature)
		}
		if got.DocComment != want.DocComment {
			t.Errorf("DocComment: got %q, want %q", got.DocComment, want.DocComment)
		}
		if got.ProjectID != want.ProjectID {
			t.Errorf("ProjectID: got %q, want %q", got.ProjectID, want.ProjectID)
		}
	}

	// Verify SearchSymbols with a name filter returns the expected symbol.
	results, err := s.SearchSymbols(SearchQuery{Name: "FooBar"})
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("SearchSymbols(FooBar): got %d results, want 1", len(results))
	}
	if results[0].ID != sym1.ID {
		t.Errorf("SearchSymbols(FooBar): got ID %q, want %q", results[0].ID, sym1.ID)
	}
}

// ─── 4. TestUpsertSymbolsDeduplication ───────────────────────────────────────

func TestUpsertSymbolsDeduplication(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")

	sym := parse.Symbol{
		ID:        "sym-dup",
		Name:      "DupSymbol",
		Kind:      parse.KindFunction,
		File:      "src/dup.go",
		StartLine: 1,
		EndLine:   5,
		ProjectID: "proj1",
	}

	// Insert the same symbol twice.
	if err := s.UpsertSymbols([]parse.Symbol{sym}); err != nil {
		t.Fatalf("first UpsertSymbols: %v", err)
	}
	if err := s.UpsertSymbols([]parse.Symbol{sym}); err != nil {
		t.Fatalf("second UpsertSymbols: %v", err)
	}

	results, err := s.SearchSymbols(SearchQuery{Name: "DupSymbol"})
	if err != nil {
		t.Fatalf("SearchSymbols: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("SearchSymbols after double insert: got %d results, want 1 (INSERT OR REPLACE semantics)", len(results))
	}
}

// ─── 5. TestDeleteByFileRemovesAllData ────────────────────────────────────────

func TestDeleteByFileRemovesAllData(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")

	// Two symbols in fileA, one symbol in fileB.
	symA1 := parse.Symbol{ID: "a1", Name: "SymA1", Kind: parse.KindFunction, File: "src/a.go", StartLine: 1, EndLine: 5, ProjectID: "proj1"}
	symA2 := parse.Symbol{ID: "a2", Name: "SymA2", Kind: parse.KindFunction, File: "src/a.go", StartLine: 6, EndLine: 10, ProjectID: "proj1"}
	symB1 := parse.Symbol{ID: "b1", Name: "SymB1", Kind: parse.KindFunction, File: "src/b.go", StartLine: 1, EndLine: 5, ProjectID: "proj1"}

	if err := s.UpsertSymbols([]parse.Symbol{symA1, symA2, symB1}); err != nil {
		t.Fatalf("UpsertSymbols: %v", err)
	}

	// Edge from a1 -> b1 (cross-file) and b1 -> a2 (cross-file).
	edges := []parse.Edge{
		{FromID: "a1", ToID: "b1", Kind: parse.EdgeCalls, File: "src/a.go", Line: 3},
		{FromID: "b1", ToID: "a2", Kind: parse.EdgeCalls, File: "src/b.go", Line: 2},
	}
	if err := s.UpsertEdges(edges); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	// Track both files in file_index.
	now := time.Now().UTC()
	if err := s.SetFileHash("src/a.go", "hashA", now); err != nil {
		t.Fatalf("SetFileHash a: %v", err)
	}
	if err := s.SetFileHash("src/b.go", "hashB", now); err != nil {
		t.Fatalf("SetFileHash b: %v", err)
	}

	// Delete fileA.
	if err := s.DeleteByFile("src/a.go"); err != nil {
		t.Fatalf("DeleteByFile: %v", err)
	}

	// (a) Symbols for fileA are gone.
	for _, id := range []string{"a1", "a2"} {
		sym, err := s.GetSymbol(id)
		if err != nil {
			t.Fatalf("GetSymbol(%s): %v", id, err)
		}
		if sym != nil {
			t.Errorf("symbol %s should have been deleted but still exists", id)
		}
	}

	// (b) Symbol for fileB remains.
	symB, err := s.GetSymbol("b1")
	if err != nil {
		t.Fatalf("GetSymbol(b1): %v", err)
	}
	if symB == nil {
		t.Errorf("symbol b1 should still exist after deleting src/a.go")
	}

	// (c) Edges involving a1 or a2 are gone.
	var edgeCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id IN ('a1','a2') OR to_id IN ('a1','a2')`).Scan(&edgeCount); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edgeCount != 0 {
		t.Errorf("expected 0 edges referencing deleted symbols, got %d", edgeCount)
	}

	// (d) file_index entry for fileA is gone.
	hash, _, err := s.GetFileHash("src/a.go")
	if err != nil {
		t.Fatalf("GetFileHash(src/a.go): %v", err)
	}
	if hash != "" {
		t.Errorf("GetFileHash(src/a.go): expected empty string after delete, got %q", hash)
	}

	// file_index entry for fileB still exists.
	hashB, _, err := s.GetFileHash("src/b.go")
	if err != nil {
		t.Fatalf("GetFileHash(src/b.go): %v", err)
	}
	if hashB != "hashB" {
		t.Errorf("GetFileHash(src/b.go): expected %q, got %q", "hashB", hashB)
	}
}

// ─── 6. TestGetCallersGetCallees ──────────────────────────────────────────────

func TestGetCallersGetCallees(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")

	// Build call graph: A -> B -> C
	symA := parse.Symbol{ID: "A", Name: "FuncA", Kind: parse.KindFunction, File: "src/a.go", StartLine: 1, EndLine: 5, ProjectID: "proj1"}
	symB := parse.Symbol{ID: "B", Name: "FuncB", Kind: parse.KindFunction, File: "src/b.go", StartLine: 1, EndLine: 5, ProjectID: "proj1"}
	symC := parse.Symbol{ID: "C", Name: "FuncC", Kind: parse.KindFunction, File: "src/c.go", StartLine: 1, EndLine: 5, ProjectID: "proj1"}

	if err := s.UpsertSymbols([]parse.Symbol{symA, symB, symC}); err != nil {
		t.Fatalf("UpsertSymbols: %v", err)
	}

	edges := []parse.Edge{
		{FromID: "A", ToID: "B", Kind: parse.EdgeCalls, File: "src/a.go", Line: 3},
		{FromID: "B", ToID: "C", Kind: parse.EdgeCalls, File: "src/b.go", Line: 3},
	}
	if err := s.UpsertEdges(edges); err != nil {
		t.Fatalf("UpsertEdges: %v", err)
	}

	// GetCallers(B, 1) → [A]
	callers, err := s.GetCallers("B", 1)
	if err != nil {
		t.Fatalf("GetCallers(B,1): %v", err)
	}
	if ids := symbolIDs(callers); len(ids) != 1 || ids[0] != "A" {
		t.Errorf("GetCallers(B,1): got %v, want [A]", ids)
	}

	// GetCallers(C, 1) → [B]
	callers, err = s.GetCallers("C", 1)
	if err != nil {
		t.Fatalf("GetCallers(C,1): %v", err)
	}
	if ids := symbolIDs(callers); len(ids) != 1 || ids[0] != "B" {
		t.Errorf("GetCallers(C,1): got %v, want [B]", ids)
	}

	// GetCallers(C, 2) → [A, B] (both, order doesn't matter)
	callers, err = s.GetCallers("C", 2)
	if err != nil {
		t.Fatalf("GetCallers(C,2): %v", err)
	}
	ids := symbolIDs(callers)
	if len(ids) != 2 {
		t.Fatalf("GetCallers(C,2): got %d results, want 2: %v", len(ids), ids)
	}
	if ids[0] != "A" || ids[1] != "B" {
		t.Errorf("GetCallers(C,2): got %v, want [A B]", ids)
	}

	// Queried symbol itself is never in the result.
	for _, sym := range callers {
		if sym.ID == "C" {
			t.Errorf("GetCallers(C,2): result contains the queried symbol C")
		}
	}

	// GetCallees(B, 1) → [C]
	callees, err := s.GetCallees("B", 1)
	if err != nil {
		t.Fatalf("GetCallees(B,1): %v", err)
	}
	if ids := symbolIDs(callees); len(ids) != 1 || ids[0] != "C" {
		t.Errorf("GetCallees(B,1): got %v, want [C]", ids)
	}

	// GetCallees(A, 1) → [B]
	callees, err = s.GetCallees("A", 1)
	if err != nil {
		t.Fatalf("GetCallees(A,1): %v", err)
	}
	if ids := symbolIDs(callees); len(ids) != 1 || ids[0] != "B" {
		t.Errorf("GetCallees(A,1): got %v, want [B]", ids)
	}

	// GetCallees(A, 2) → [B, C]
	callees, err = s.GetCallees("A", 2)
	if err != nil {
		t.Fatalf("GetCallees(A,2): %v", err)
	}
	ids = symbolIDs(callees)
	if len(ids) != 2 {
		t.Fatalf("GetCallees(A,2): got %d results, want 2: %v", len(ids), ids)
	}
	if ids[0] != "B" || ids[1] != "C" {
		t.Errorf("GetCallees(A,2): got %v, want [B C]", ids)
	}

	// Queried symbol itself is never in the result.
	for _, sym := range callees {
		if sym.ID == "A" {
			t.Errorf("GetCallees(A,2): result contains the queried symbol A")
		}
	}
}

// ─── 7. TestFullTextSearchFindsInsertedSymbols ────────────────────────────────

func TestFullTextSearchFindsInsertedSymbols(t *testing.T) {
	s := openTestStore(t)
	insertProject(t, s, "proj1")

	sym := parse.Symbol{
		ID:        "fts-001",
		Name:      "handlePaymentRequest",
		Kind:      parse.KindFunction,
		File:      "src/payment.go",
		StartLine: 1,
		EndLine:   30,
		Signature: "func handlePaymentRequest(ctx context.Context, req *PaymentRequest) error",
		ProjectID: "proj1",
	}

	if err := s.UpsertSymbols([]parse.Symbol{sym}); err != nil {
		t.Fatalf("UpsertSymbols: %v", err)
	}

	// Search for the distinctive name.
	results, err := s.FullTextSearch("handlePaymentRequest", 10)
	if err != nil {
		t.Fatalf("FullTextSearch: %v", err)
	}

	found := false
	for _, r := range results {
		if r.ID == sym.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FullTextSearch(handlePaymentRequest): symbol not found in results %v", symbolIDs(results))
	}

	// Search for a non-existent term returns an empty slice.
	empty, err := s.FullTextSearch("xyzzy_nonexistent_term_42", 10)
	if err != nil {
		t.Fatalf("FullTextSearch(nonexistent): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("FullTextSearch(nonexistent): expected 0 results, got %d", len(empty))
	}
}

// ─── 8. TestGetFileHashAfterSetFileHash ───────────────────────────────────────

func TestGetFileHashAfterSetFileHash(t *testing.T) {
	s := openTestStore(t)

	// Use a truncated time to avoid sub-second precision issues with RFC3339.
	modTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	if err := s.SetFileHash("src/main.go", "abc123", modTime); err != nil {
		t.Fatalf("SetFileHash: %v", err)
	}

	hash, gotTime, err := s.GetFileHash("src/main.go")
	if err != nil {
		t.Fatalf("GetFileHash: %v", err)
	}
	if hash != "abc123" {
		t.Errorf("hash: got %q, want %q", hash, "abc123")
	}
	if !gotTime.UTC().Equal(modTime.UTC()) {
		t.Errorf("modTime: got %v, want %v", gotTime.UTC(), modTime.UTC())
	}

	// GetFileHash on an untracked path returns ("", zero time, nil).
	h2, t2, err := s.GetFileHash("src/untracked.go")
	if err != nil {
		t.Fatalf("GetFileHash(untracked): %v", err)
	}
	if h2 != "" {
		t.Errorf("untracked hash: got %q, want empty string", h2)
	}
	if !t2.IsZero() {
		t.Errorf("untracked modTime: got %v, want zero time", t2)
	}
}

// ─── Property Tests (task 5.16) ───────────────────────────────────────────────
//
// These tests use pgregory.net/rapid to verify correctness properties of the
// Graph Store across arbitrary inputs.

// makeSymbol is a rapid generator that produces a valid parse.Symbol with the
// given projectID and file path. The idx parameter ensures distinct IDs across
// multiple symbols generated in the same test run.
func makeSymbol(rt *rapid.T, projectID, file string, idx int) parse.Symbol {
	rt.Helper()
	kind := rapid.SampledFrom([]parse.SymbolKind{
		parse.KindFunction, parse.KindMethod, parse.KindType,
		parse.KindInterface, parse.KindVariable, parse.KindModule, parse.KindClass,
	}).Draw(rt, "kind")
	name := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_]{0,19}`).Draw(rt, "name")
	startLine := rapid.IntRange(1, 500).Draw(rt, "startLine")
	endLine := startLine + rapid.IntRange(0, 50).Draw(rt, "endLineDelta")
	sig := rapid.StringMatching(`[a-zA-Z0-9 _(),]{0,60}`).Draw(rt, "sig")
	doc := rapid.StringMatching(`[a-zA-Z0-9 .,]{0,80}`).Draw(rt, "doc")

	// Build a unique ID: embed the index in the first 4 hex chars so symbols
	// within the same test never collide.
	rawID := rapid.StringMatching(`[a-f0-9]{28}`).Draw(rt, "rawID")
	id := fmt.Sprintf("%04x", idx) + rawID

	return parse.Symbol{
		ID:         id,
		Name:       name,
		Kind:       kind,
		File:       file,
		StartLine:  startLine,
		EndLine:    endLine,
		Signature:  sig,
		DocComment: doc,
		ProjectID:  projectID,
	}
}

// openPropertyStore opens a fresh in-memory store for use inside a rapid
// property test. It calls rt.Fatal on any setup error.
func openPropertyStore(rt *rapid.T) *Store {
	rt.Helper()
	s, err := Open(":memory:")
	if err != nil {
		rt.Fatalf("Open: %v", err)
	}
	if err := s.Migrate(); err != nil {
		s.Close()
		rt.Fatalf("Migrate: %v", err)
	}
	return s
}

// insertProjectRT inserts a minimal project row inside a rapid test.
func insertProjectRT(rt *rapid.T, s *Store, id string) {
	rt.Helper()
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO projects (id, name, path, language) VALUES (?, ?, ?, ?)`,
		id, id, "/"+id, "go",
	)
	if err != nil {
		rt.Fatalf("insertProjectRT %s: %v", id, err)
	}
}

// ─── Property 8: Upsert idempotency ──────────────────────────────────────────

// TestProperty_UpsertSymbolIdempotent verifies that upserting the same symbol
// twice results in exactly one copy in the DB (INSERT OR REPLACE semantics).
//
// Validates: Requirements 3.3, 3.4
func TestProperty_UpsertSymbolIdempotent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := openPropertyStore(rt)
		defer s.Close()
		insertProjectRT(rt, s, "proj1")

		sym := makeSymbol(rt, "proj1", "src/foo.go", 0)

		// Upsert the same symbol twice (possibly with mutated signature/doc).
		if err := s.UpsertSymbols([]parse.Symbol{sym}); err != nil {
			rt.Fatalf("first UpsertSymbols: %v", err)
		}

		// Optionally mutate non-ID fields to simulate a re-parse.
		sym.Signature = rapid.StringMatching(`[a-zA-Z0-9 _(),]{0,60}`).Draw(rt, "newSig")
		sym.DocComment = rapid.StringMatching(`[a-zA-Z0-9 .,]{0,80}`).Draw(rt, "newDoc")

		if err := s.UpsertSymbols([]parse.Symbol{sym}); err != nil {
			rt.Fatalf("second UpsertSymbols: %v", err)
		}

		// Count rows with this ID — must be exactly 1.
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE id = ?`, sym.ID).Scan(&count); err != nil {
			rt.Fatalf("count symbols: %v", err)
		}
		if count != 1 {
			rt.Fatalf("expected exactly 1 symbol after double upsert, got %d (id=%q)", count, sym.ID)
		}

		// FTS index must also have exactly one entry for this ID.
		var ftsCount int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM symbols_fts WHERE id = ?`, sym.ID).Scan(&ftsCount); err != nil {
			rt.Fatalf("count symbols_fts: %v", err)
		}
		if ftsCount != 1 {
			rt.Fatalf("expected exactly 1 FTS entry after double upsert, got %d (id=%q)", ftsCount, sym.ID)
		}
	})
}

// ─── Property 9: DeleteByFile completeness ────────────────────────────────────

// TestProperty_DeleteByFileRemovesAll verifies that after DeleteByFile, no
// symbols or edges for that file remain in the store.
//
// Validates: Requirements 3.6, 4.4
func TestProperty_DeleteByFileRemovesAll(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := openPropertyStore(rt)
		defer s.Close()
		insertProjectRT(rt, s, "proj1")

		targetFile := "src/target.go"
		otherFile := "src/other.go"

		// Generate 1–5 symbols in the target file and 1–3 in another file.
		numTarget := rapid.IntRange(1, 5).Draw(rt, "numTarget")
		numOther := rapid.IntRange(1, 3).Draw(rt, "numOther")

		targetSyms := make([]parse.Symbol, numTarget)
		for i := 0; i < numTarget; i++ {
			targetSyms[i] = makeSymbol(rt, "proj1", targetFile, i)
		}
		otherSyms := make([]parse.Symbol, numOther)
		for i := 0; i < numOther; i++ {
			otherSyms[i] = makeSymbol(rt, "proj1", otherFile, numTarget+i)
		}

		allSyms := append(targetSyms, otherSyms...)
		if err := s.UpsertSymbols(allSyms); err != nil {
			rt.Fatalf("UpsertSymbols: %v", err)
		}

		// Add an edge from a target symbol to an other symbol.
		if len(targetSyms) > 0 && len(otherSyms) > 0 {
			edge := parse.Edge{
				FromID: targetSyms[0].ID,
				ToID:   otherSyms[0].ID,
				Kind:   parse.EdgeCalls,
				File:   targetFile,
				Line:   1,
			}
			if err := s.UpsertEdges([]parse.Edge{edge}); err != nil {
				rt.Fatalf("UpsertEdges: %v", err)
			}
		}

		// Track the target file.
		if err := s.SetFileHash(targetFile, "hash1", time.Now().UTC()); err != nil {
			rt.Fatalf("SetFileHash: %v", err)
		}

		// Delete the target file.
		if err := s.DeleteByFile(targetFile); err != nil {
			rt.Fatalf("DeleteByFile: %v", err)
		}

		// No symbols for the target file should remain.
		var symCount int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE file = ?`, targetFile).Scan(&symCount); err != nil {
			rt.Fatalf("count symbols: %v", err)
		}
		if symCount != 0 {
			rt.Fatalf("expected 0 symbols for %q after DeleteByFile, got %d", targetFile, symCount)
		}

		// No edges originating from or pointing to target symbols should remain.
		for _, sym := range targetSyms {
			var edgeCount int
			if err := s.db.QueryRow(
				`SELECT COUNT(*) FROM edges WHERE from_id = ? OR to_id = ?`, sym.ID, sym.ID,
			).Scan(&edgeCount); err != nil {
				rt.Fatalf("count edges for %q: %v", sym.ID, err)
			}
			if edgeCount != 0 {
				rt.Fatalf("expected 0 edges for deleted symbol %q, got %d", sym.ID, edgeCount)
			}
		}

		// No edges with file = targetFile should remain.
		var fileEdgeCount int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM edges WHERE file = ?`, targetFile).Scan(&fileEdgeCount); err != nil {
			rt.Fatalf("count file edges: %v", err)
		}
		if fileEdgeCount != 0 {
			rt.Fatalf("expected 0 edges with file=%q after DeleteByFile, got %d", targetFile, fileEdgeCount)
		}

		// file_index entry for the target file should be gone.
		hash, _, err := s.GetFileHash(targetFile)
		if err != nil {
			rt.Fatalf("GetFileHash: %v", err)
		}
		if hash != "" {
			rt.Fatalf("expected empty hash for %q after DeleteByFile, got %q", targetFile, hash)
		}

		// Symbols in the other file must still be present.
		var otherCount int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM symbols WHERE file = ?`, otherFile).Scan(&otherCount); err != nil {
			rt.Fatalf("count other symbols: %v", err)
		}
		if otherCount != numOther {
			rt.Fatalf("expected %d symbols in %q to survive, got %d", numOther, otherFile, otherCount)
		}
	})
}

// ─── Property 10: SetFileHash / GetFileHash round-trip ────────────────────────

// TestProperty_FileHashRoundTrip verifies that for any (path, hash, modTime),
// SetFileHash then GetFileHash returns the same values.
//
// Validates: Requirement 3.7
func TestProperty_FileHashRoundTrip(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := openPropertyStore(rt)
		defer s.Close()

		path := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9/_.-]{1,40}\.go`).Draw(rt, "path")
		hash := rapid.StringMatching(`[a-f0-9]{64}`).Draw(rt, "hash")

		// Generate a time with second-level precision to avoid RFC3339 round-trip
		// issues with sub-second values.
		year := rapid.IntRange(2000, 2030).Draw(rt, "year")
		month := rapid.IntRange(1, 12).Draw(rt, "month")
		day := rapid.IntRange(1, 28).Draw(rt, "day")
		hour := rapid.IntRange(0, 23).Draw(rt, "hour")
		min := rapid.IntRange(0, 59).Draw(rt, "min")
		sec := rapid.IntRange(0, 59).Draw(rt, "sec")
		modTime := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.UTC)

		if err := s.SetFileHash(path, hash, modTime); err != nil {
			rt.Fatalf("SetFileHash: %v", err)
		}

		gotHash, gotTime, err := s.GetFileHash(path)
		if err != nil {
			rt.Fatalf("GetFileHash: %v", err)
		}
		if gotHash != hash {
			rt.Fatalf("hash mismatch: stored %q, retrieved %q", hash, gotHash)
		}
		if !gotTime.UTC().Equal(modTime.UTC()) {
			rt.Fatalf("modTime mismatch: stored %v, retrieved %v", modTime.UTC(), gotTime.UTC())
		}
	})
}

// ─── Property 7: FullTextSearch finds inserted symbol ─────────────────────────

// TestProperty_FullTextSearchFindsSymbol verifies that for any symbol inserted
// into the store, FullTextSearch(symbol.Name) returns a result set containing
// that symbol.
//
// Validates: Requirement 6.11
func TestProperty_FullTextSearchFindsSymbol(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := openPropertyStore(rt)
		defer s.Close()
		insertProjectRT(rt, s, "proj1")

		// Use a name that is a single distinct word so FTS5 can match it exactly.
		// FTS5 tokenises on whitespace/punctuation, so we use a plain identifier.
		name := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9]{4,19}`).Draw(rt, "name")

		sym := parse.Symbol{
			ID:        rapid.StringMatching(`[a-f0-9]{32}`).Draw(rt, "id"),
			Name:      name,
			Kind:      parse.KindFunction,
			File:      "src/prop.go",
			StartLine: 1,
			EndLine:   10,
			ProjectID: "proj1",
		}

		if err := s.UpsertSymbols([]parse.Symbol{sym}); err != nil {
			rt.Fatalf("UpsertSymbols: %v", err)
		}

		results, err := s.FullTextSearch(name, 100)
		if err != nil {
			rt.Fatalf("FullTextSearch(%q): %v", name, err)
		}

		found := false
		for _, r := range results {
			if r.ID == sym.ID {
				found = true
				break
			}
		}
		if !found {
			rt.Fatalf("FullTextSearch(%q): symbol %q not found in results (got %d results)", name, sym.ID, len(results))
		}
	})
}

// ─── Property 3: Caller/Callee symmetry ──────────────────────────────────────

// TestProperty_CallerCalleeSym verifies that for any two symbols A and B where
// a calls edge exists from A to B, A appears in GetCallers(B) and B appears in
// GetCallees(A).
//
// Validates: Requirements 6.1, 6.2
func TestProperty_CallerCalleeSym(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := openPropertyStore(rt)
		defer s.Close()
		insertProjectRT(rt, s, "proj1")

		// Generate a chain of 2–5 symbols.
		n := rapid.IntRange(2, 5).Draw(rt, "n")
		syms := make([]parse.Symbol, n)
		for i := 0; i < n; i++ {
			syms[i] = makeSymbol(rt, "proj1", fmt.Sprintf("src/f%d.go", i), i)
		}
		if err := s.UpsertSymbols(syms); err != nil {
			rt.Fatalf("UpsertSymbols: %v", err)
		}

		// Build consecutive calls edges: syms[0]->syms[1]->...->syms[n-1].
		edges := make([]parse.Edge, n-1)
		for i := 0; i < n-1; i++ {
			edges[i] = parse.Edge{
				FromID: syms[i].ID,
				ToID:   syms[i+1].ID,
				Kind:   parse.EdgeCalls,
				File:   fmt.Sprintf("src/f%d.go", i),
				Line:   1,
			}
		}
		if err := s.UpsertEdges(edges); err != nil {
			rt.Fatalf("UpsertEdges: %v", err)
		}

		// For each direct edge A->B, verify symmetry at depth 1.
		for _, e := range edges {
			// A must appear in GetCallers(B, 1).
			callers, err := s.GetCallers(e.ToID, 1)
			if err != nil {
				rt.Fatalf("GetCallers(%q, 1): %v", e.ToID, err)
			}
			foundA := false
			for _, c := range callers {
				if c.ID == e.FromID {
					foundA = true
					break
				}
			}
			if !foundA {
				rt.Fatalf("edge %q->%q: caller %q not found in GetCallers(%q, 1)", e.FromID, e.ToID, e.FromID, e.ToID)
			}

			// B must appear in GetCallees(A, 1).
			callees, err := s.GetCallees(e.FromID, 1)
			if err != nil {
				rt.Fatalf("GetCallees(%q, 1): %v", e.FromID, err)
			}
			foundB := false
			for _, c := range callees {
				if c.ID == e.ToID {
					foundB = true
					break
				}
			}
			if !foundB {
				rt.Fatalf("edge %q->%q: callee %q not found in GetCallees(%q, 1)", e.FromID, e.ToID, e.ToID, e.FromID)
			}
		}
	})
}

// ─── Property 6: Traversal depth bound ───────────────────────────────────────

// shortestCallPath returns the shortest number of hops from `from` to `to`
// in the given edge list via calls edges, or -1 if unreachable.
func shortestCallPath(from, to string, edges []parse.Edge) int {
	if from == to {
		return 0
	}
	type state struct {
		id    string
		depth int
	}
	visited := map[string]bool{from: true}
	queue := []state{{from, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range edges {
			if e.Kind != parse.EdgeCalls || e.FromID != cur.id {
				continue
			}
			if e.ToID == to {
				return cur.depth + 1
			}
			if !visited[e.ToID] {
				visited[e.ToID] = true
				queue = append(queue, state{e.ToID, cur.depth + 1})
			}
		}
	}
	return -1
}

// TestProperty_TraversalDepthBound verifies that for any traversal query with
// depth D, no result symbol has a shortest call path to the target exceeding D.
//
// Validates: Requirement 6.5
func TestProperty_TraversalDepthBound(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := openPropertyStore(rt)
		defer s.Close()
		insertProjectRT(rt, s, "proj1")

		// Build a chain of 4 symbols: S0 -> S1 -> S2 -> S3.
		const chainLen = 4
		syms := make([]parse.Symbol, chainLen)
		for i := 0; i < chainLen; i++ {
			syms[i] = makeSymbol(rt, "proj1", fmt.Sprintf("src/d%d.go", i), i)
		}
		if err := s.UpsertSymbols(syms); err != nil {
			rt.Fatalf("UpsertSymbols: %v", err)
		}

		chainEdges := make([]parse.Edge, chainLen-1)
		for i := 0; i < chainLen-1; i++ {
			chainEdges[i] = parse.Edge{
				FromID: syms[i].ID,
				ToID:   syms[i+1].ID,
				Kind:   parse.EdgeCalls,
				File:   fmt.Sprintf("src/d%d.go", i),
				Line:   1,
			}
		}
		if err := s.UpsertEdges(chainEdges); err != nil {
			rt.Fatalf("UpsertEdges: %v", err)
		}

		// Pick a random depth in [1, chainLen-1] and query callers of the last symbol.
		depth := rapid.IntRange(1, chainLen-1).Draw(rt, "depth")
		target := syms[chainLen-1]

		callers, err := s.GetCallers(target.ID, depth)
		if err != nil {
			rt.Fatalf("GetCallers(%q, %d): %v", target.ID, depth, err)
		}
		for _, caller := range callers {
			d := shortestCallPath(caller.ID, target.ID, chainEdges)
			if d < 0 {
				rt.Fatalf("GetCallers returned symbol %q with no path to target %q", caller.ID, target.ID)
			}
			if d > depth {
				rt.Fatalf(
					"GetCallers(%q, %d) returned symbol %q whose shortest path is %d (exceeds depth)",
					target.ID, depth, caller.ID, d,
				)
			}
		}

		// Also verify callees of the first symbol.
		source := syms[0]
		callees, err := s.GetCallees(source.ID, depth)
		if err != nil {
			rt.Fatalf("GetCallees(%q, %d): %v", source.ID, depth, err)
		}
		for _, callee := range callees {
			d := shortestCallPath(source.ID, callee.ID, chainEdges)
			if d < 0 {
				rt.Fatalf("GetCallees returned symbol %q with no path from source %q", callee.ID, source.ID)
			}
			if d > depth {
				rt.Fatalf(
					"GetCallees(%q, %d) returned symbol %q whose shortest path is %d (exceeds depth)",
					source.ID, depth, callee.ID, d,
				)
			}
		}
	})
}

// ─── Property 14: Queried symbol excluded from traversal results ──────────────

// TestProperty_TraversalExcludesTarget verifies that for any traversal query,
// the queried symbol ID does not appear in the result set.
//
// Validates: Requirement 6.3
func TestProperty_TraversalExcludesTarget(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := openPropertyStore(rt)
		defer s.Close()
		insertProjectRT(rt, s, "proj1")

		// Build a cycle A -> B -> C -> A so the target could theoretically appear
		// in results if the exclusion logic is broken.
		symA := makeSymbol(rt, "proj1", "src/a.go", 0)
		symB := makeSymbol(rt, "proj1", "src/b.go", 1)
		symC := makeSymbol(rt, "proj1", "src/c.go", 2)

		if err := s.UpsertSymbols([]parse.Symbol{symA, symB, symC}); err != nil {
			rt.Fatalf("UpsertSymbols: %v", err)
		}

		cycleEdges := []parse.Edge{
			{FromID: symA.ID, ToID: symB.ID, Kind: parse.EdgeCalls, File: "src/a.go", Line: 1},
			{FromID: symB.ID, ToID: symC.ID, Kind: parse.EdgeCalls, File: "src/b.go", Line: 1},
			{FromID: symC.ID, ToID: symA.ID, Kind: parse.EdgeCalls, File: "src/c.go", Line: 1},
		}
		if err := s.UpsertEdges(cycleEdges); err != nil {
			rt.Fatalf("UpsertEdges: %v", err)
		}

		depth := rapid.IntRange(1, 5).Draw(rt, "depth")

		for _, target := range []parse.Symbol{symA, symB, symC} {
			// GetCallers — target must not appear in results.
			callers, err := s.GetCallers(target.ID, depth)
			if err != nil {
				rt.Fatalf("GetCallers(%q, %d): %v", target.ID, depth, err)
			}
			for _, c := range callers {
				if c.ID == target.ID {
					rt.Fatalf("GetCallers(%q, %d): result contains the queried symbol itself", target.ID, depth)
				}
			}

			// GetCallees — target must not appear in results.
			callees, err := s.GetCallees(target.ID, depth)
			if err != nil {
				rt.Fatalf("GetCallees(%q, %d): %v", target.ID, depth, err)
			}
			for _, c := range callees {
				if c.ID == target.ID {
					rt.Fatalf("GetCallees(%q, %d): result contains the queried symbol itself", target.ID, depth)
				}
			}
		}
	})
}

// ─── Property 15: No duplicate Symbol IDs in traversal results ───────────────

// TestProperty_TraversalNoDuplicates verifies that for any traversal query,
// the result set contains no duplicate Symbol IDs.
//
// Validates: Requirement 6.4
func TestProperty_TraversalNoDuplicates(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		s := openPropertyStore(rt)
		defer s.Close()
		insertProjectRT(rt, s, "proj1")

		// Build a diamond graph: A -> B, A -> C, B -> D, C -> D.
		// D has two paths from A, which could produce duplicates if DISTINCT is missing.
		symA := makeSymbol(rt, "proj1", "src/a.go", 0)
		symB := makeSymbol(rt, "proj1", "src/b.go", 1)
		symC := makeSymbol(rt, "proj1", "src/c.go", 2)
		symD := makeSymbol(rt, "proj1", "src/d.go", 3)

		if err := s.UpsertSymbols([]parse.Symbol{symA, symB, symC, symD}); err != nil {
			rt.Fatalf("UpsertSymbols: %v", err)
		}

		diamondEdges := []parse.Edge{
			{FromID: symA.ID, ToID: symB.ID, Kind: parse.EdgeCalls, File: "src/a.go", Line: 1},
			{FromID: symA.ID, ToID: symC.ID, Kind: parse.EdgeCalls, File: "src/a.go", Line: 2},
			{FromID: symB.ID, ToID: symD.ID, Kind: parse.EdgeCalls, File: "src/b.go", Line: 1},
			{FromID: symC.ID, ToID: symD.ID, Kind: parse.EdgeCalls, File: "src/c.go", Line: 1},
		}
		if err := s.UpsertEdges(diamondEdges); err != nil {
			rt.Fatalf("UpsertEdges: %v", err)
		}

		depth := rapid.IntRange(1, 5).Draw(rt, "depth")

		// GetCallers(D) — no duplicate IDs.
		callers, err := s.GetCallers(symD.ID, depth)
		if err != nil {
			rt.Fatalf("GetCallers(%q, %d): %v", symD.ID, depth, err)
		}
		seen := make(map[string]bool, len(callers))
		for _, c := range callers {
			if seen[c.ID] {
				rt.Fatalf("GetCallers(%q, %d): duplicate symbol ID %q in results", symD.ID, depth, c.ID)
			}
			seen[c.ID] = true
		}

		// GetCallees(A) — no duplicate IDs.
		callees, err := s.GetCallees(symA.ID, depth)
		if err != nil {
			rt.Fatalf("GetCallees(%q, %d): %v", symA.ID, depth, err)
		}
		seen = make(map[string]bool, len(callees))
		for _, c := range callees {
			if seen[c.ID] {
				rt.Fatalf("GetCallees(%q, %d): duplicate symbol ID %q in results", symA.ID, depth, c.ID)
			}
			seen[c.ID] = true
		}
	})
}
