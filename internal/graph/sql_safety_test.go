package graph

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoSQLStringInterpolation audits all .go files in the internal/graph
// package and fails if any SQL string is constructed via fmt.Sprintf or unsafe
// string concatenation with a SQL keyword.
//
// This implements Requirement 11.4: the Graph_Store must use parameterized SQL
// statements for all queries and must NOT construct SQL strings by interpolating
// user-provided values.
func TestNoSQLStringInterpolation(t *testing.T) {
	// Locate the package directory relative to this test file.
	// When tests run, the working directory is the package directory.
	pkgDir := "."

	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", pkgDir, err)
	}

	fset := token.NewFileSet()
	var violations []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		// Skip test files themselves to avoid false positives from test strings.
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		path := filepath.Join(pkgDir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", path, err)
		}

		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("ParseFile(%q): %v", path, err)
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				// Check for fmt.Sprintf calls where the format string contains SQL keywords.
				if isFmtSprintf(node) {
					if formatArgContainsSQLKeyword(node) {
						pos := fset.Position(node.Pos())
						violations = append(violations,
							pos.String()+": fmt.Sprintf used to construct SQL string")
					}
				}

			case *ast.BinaryExpr:
				// Check for string concatenation (+) where one operand is a SQL string
				// literal and the other is an unsafe (user-data-bearing) expression.
				// Safe patterns (not flagged):
				//   - Both sides are string literals (pure SQL structure)
				//   - The non-literal side is a strings.Join call (joins pre-defined
				//     parameterized condition strings, not user-provided values)
				//   - The non-literal side is another binary + expression (chained
				//     SQL structure; each sub-expression is checked separately)
				if node.Op == token.ADD {
					if binaryExprContainsUnsafeSQLConcatenation(node) {
						pos := fset.Position(node.Pos())
						violations = append(violations,
							pos.String()+": string concatenation (+) used to construct SQL string with non-literal value")
					}
				}
			}
			return true
		})
	}

	if len(violations) > 0 {
		t.Errorf("Found %d potential SQL injection vulnerabilities in internal/graph/ (non-test files):", len(violations))
		for _, v := range violations {
			t.Errorf("  %s", v)
		}
		t.Error("All SQL queries must use parameterized statements (? placeholders) instead of string interpolation.")
	}
}

// sqlKeywords is the set of SQL keywords that indicate a string is likely a
// SQL query fragment.
var sqlKeywords = []string{
	"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER",
	"WHERE", "FROM", "JOIN", "INTO", "VALUES", "TABLE", "INDEX",
}

// isFmtSprintf returns true if the call expression is fmt.Sprintf.
func isFmtSprintf(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "fmt" && sel.Sel.Name == "Sprintf"
}

// formatArgContainsSQLKeyword returns true if the first argument to fmt.Sprintf
// is a string literal containing a SQL keyword.
func formatArgContainsSQLKeyword(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	upper := strings.ToUpper(lit.Value)
	for _, kw := range sqlKeywords {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

// binaryExprContainsUnsafeSQLConcatenation returns true if a binary +
// expression looks like unsafe SQL construction: one side is a SQL keyword
// string literal and the other side is a non-literal expression that could
// inject user-provided data.
//
// Safe patterns (returns false):
//   - Both sides are string literals (pure SQL structure, no user data)
//   - The non-literal side is a strings.Join call (joins pre-defined
//     parameterized condition strings, not user-provided values)
//   - The non-literal side is another binary + expression (chained SQL
//     structure; each sub-expression is checked separately by the AST walker)
func binaryExprContainsUnsafeSQLConcatenation(expr *ast.BinaryExpr) bool {
	xIsSQL := stringLitContainsSQLKeyword(expr.X)
	yIsSQL := stringLitContainsSQLKeyword(expr.Y)

	if !xIsSQL && !yIsSQL {
		return false
	}

	// If both sides are literals, this is pure SQL structure — safe.
	_, xIsLit := expr.X.(*ast.BasicLit)
	_, yIsLit := expr.Y.(*ast.BasicLit)
	if xIsLit && yIsLit {
		return false
	}

	// If the non-literal side is a strings.Join call, it joins pre-defined
	// parameterized condition strings — safe.
	if xIsSQL && isStringsJoinCall(expr.Y) {
		return false
	}
	if yIsSQL && isStringsJoinCall(expr.X) {
		return false
	}

	// If the non-literal side is another binary expression (chained SQL
	// structure building), it will be checked separately by the AST walker.
	if xIsSQL {
		if _, ok := expr.Y.(*ast.BinaryExpr); ok {
			return false
		}
	}
	if yIsSQL {
		if _, ok := expr.X.(*ast.BinaryExpr); ok {
			return false
		}
	}

	return true
}

// isStringsJoinCall returns true if the node is a call to strings.Join.
func isStringsJoinCall(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "strings" && sel.Sel.Name == "Join"
}

// stringLitContainsSQLKeyword returns true if the node is a string literal
// containing a SQL keyword.
func stringLitContainsSQLKeyword(n ast.Node) bool {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	upper := strings.ToUpper(lit.Value)
	for _, kw := range sqlKeywords {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}
