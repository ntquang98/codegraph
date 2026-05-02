package parse

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// isCGoError returns true if the error indicates CGo is not available.
// This allows tests to skip gracefully in non-CGo builds.
func isCGoError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "requires CGo")
}

// ---- Task 4.10: Unit tests for each extractor --------------------------------

// TestGoExtractor_BasicSymbols verifies that the Go extractor extracts at least
// one function/method symbol, that Symbol.File equals the path, and that
// Symbol IDs are non-empty.
func TestGoExtractor_BasicSymbols(t *testing.T) {
	src := []byte(`package main

import "fmt"

type Greeter interface {
    Greet(name string) string
}

type SimpleGreeter struct{}

func (g *SimpleGreeter) Greet(name string) string {
    return fmt.Sprintf("Hello, %s!", name)
}

func main() {
    g := &SimpleGreeter{}
    g.Greet("World")
}
`)
	const path = "testdata/greeter.go"
	e := &GoExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("GoExtractor.Extract() error: %v", err)
	}

	// Verify at least one function or method symbol is extracted
	hasFuncOrMethod := false
	for _, sym := range symbols {
		if sym.Kind == KindFunction || sym.Kind == KindMethod {
			hasFuncOrMethod = true
			break
		}
	}
	if !hasFuncOrMethod {
		t.Errorf("GoExtractor: expected at least one function/method symbol, got: %+v", symbols)
	}

	// Verify all Symbol.File fields equal the input path
	for _, sym := range symbols {
		if sym.File != path {
			t.Errorf("GoExtractor: Symbol.File = %q, want %q (symbol: %q)", sym.File, path, sym.Name)
		}
	}

	// Verify all Symbol IDs are non-empty
	for _, sym := range symbols {
		if sym.ID == "" {
			t.Errorf("GoExtractor: Symbol.ID is empty for symbol %q", sym.Name)
		}
	}
}

// TestGoExtractor_InterfaceAndType verifies that the Go extractor extracts
// interface and type symbols from the sample source.
func TestGoExtractor_InterfaceAndType(t *testing.T) {
	src := []byte(`package main

import "fmt"

type Greeter interface {
    Greet(name string) string
}

type SimpleGreeter struct{}

func (g *SimpleGreeter) Greet(name string) string {
    return fmt.Sprintf("Hello, %s!", name)
}

func main() {
    g := &SimpleGreeter{}
    g.Greet("World")
}
`)
	const path = "testdata/greeter.go"
	e := &GoExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("GoExtractor.Extract() error: %v", err)
	}

	hasInterface := false
	hasType := false
	for _, sym := range symbols {
		if sym.Kind == KindInterface {
			hasInterface = true
		}
		if sym.Kind == KindType {
			hasType = true
		}
	}
	if !hasInterface {
		t.Errorf("GoExtractor: expected at least one interface symbol")
	}
	if !hasType {
		t.Errorf("GoExtractor: expected at least one type symbol")
	}
}

// TestTypeScriptExtractor_BasicSymbols verifies that the TypeScript extractor
// extracts class, interface, and function symbols from a small TS snippet.
func TestTypeScriptExtractor_BasicSymbols(t *testing.T) {
	src := []byte(`interface Animal {
    name: string;
}

class Dog implements Animal {
    name: string;
    constructor(name: string) { this.name = name; }
    bark(): void { console.log("Woof!"); }
}

function createDog(name: string): Dog {
    return new Dog(name);
}
`)
	const path = "testdata/animals.ts"
	e := &TypeScriptExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("TypeScriptExtractor.Extract() error: %v", err)
	}

	hasClass := false
	hasInterface := false
	hasFunction := false
	for _, sym := range symbols {
		switch sym.Kind {
		case KindClass:
			hasClass = true
		case KindInterface:
			hasInterface = true
		case KindFunction:
			hasFunction = true
		}
	}
	if !hasClass {
		t.Errorf("TypeScriptExtractor: expected at least one class symbol, got: %+v", symbols)
	}
	if !hasInterface {
		t.Errorf("TypeScriptExtractor: expected at least one interface symbol, got: %+v", symbols)
	}
	if !hasFunction {
		t.Errorf("TypeScriptExtractor: expected at least one function symbol, got: %+v", symbols)
	}

	// Verify all Symbol.File fields equal the input path
	for _, sym := range symbols {
		if sym.File != path {
			t.Errorf("TypeScriptExtractor: Symbol.File = %q, want %q (symbol: %q)", sym.File, path, sym.Name)
		}
	}

	// Verify all Symbol IDs are non-empty
	for _, sym := range symbols {
		if sym.ID == "" {
			t.Errorf("TypeScriptExtractor: Symbol.ID is empty for symbol %q", sym.Name)
		}
	}
}

// TestPythonExtractor_BasicSymbols verifies that the Python extractor extracts
// class and function symbols from a small Python snippet.
func TestPythonExtractor_BasicSymbols(t *testing.T) {
	src := []byte(`class Calculator:
    def add(self, a, b):
        return a + b

    def multiply(self, a, b):
        return a * b

def main():
    calc = Calculator()
    calc.add(1, 2)
`)
	const path = "testdata/calculator.py"
	e := &PythonExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("PythonExtractor.Extract() error: %v", err)
	}

	hasClass := false
	hasFunction := false
	hasMethod := false
	for _, sym := range symbols {
		switch sym.Kind {
		case KindClass:
			hasClass = true
		case KindFunction:
			hasFunction = true
		case KindMethod:
			hasMethod = true
		}
	}
	if !hasClass {
		t.Errorf("PythonExtractor: expected at least one class symbol, got: %+v", symbols)
	}
	if !hasFunction {
		t.Errorf("PythonExtractor: expected at least one function symbol, got: %+v", symbols)
	}
	if !hasMethod {
		t.Errorf("PythonExtractor: expected at least one method symbol, got: %+v", symbols)
	}

	// Verify all Symbol.File fields equal the input path
	for _, sym := range symbols {
		if sym.File != path {
			t.Errorf("PythonExtractor: Symbol.File = %q, want %q (symbol: %q)", sym.File, path, sym.Name)
		}
	}

	// Verify all Symbol IDs are non-empty
	for _, sym := range symbols {
		if sym.ID == "" {
			t.Errorf("PythonExtractor: Symbol.ID is empty for symbol %q", sym.Name)
		}
	}
}

// TestJavaScriptExtractor_BasicSymbols verifies that the JavaScript extractor
// extracts class, method, and function symbols from a small JS snippet.
func TestJavaScriptExtractor_BasicSymbols(t *testing.T) {
	src := []byte(`class EventEmitter {
    constructor() {
        this.listeners = {};
    }

    on(event, callback) {
        if (!this.listeners[event]) {
            this.listeners[event] = [];
        }
        this.listeners[event].push(callback);
    }

    emit(event, data) {
        const handlers = this.listeners[event] || [];
        handlers.forEach(fn => fn(data));
    }
}

function createEmitter() {
    return new EventEmitter();
}
`)
	const path = "testdata/emitter.js"
	e := &JavaScriptExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("JavaScriptExtractor.Extract() error: %v", err)
	}

	hasClass := false
	hasFunction := false
	for _, sym := range symbols {
		switch sym.Kind {
		case KindClass:
			hasClass = true
		case KindFunction:
			hasFunction = true
		}
	}
	if !hasClass {
		t.Errorf("JavaScriptExtractor: expected at least one class symbol, got: %+v", symbols)
	}
	if !hasFunction {
		t.Errorf("JavaScriptExtractor: expected at least one function symbol, got: %+v", symbols)
	}

	// Verify all Symbol.File fields equal the input path
	for _, sym := range symbols {
		if sym.File != path {
			t.Errorf("JavaScriptExtractor: Symbol.File = %q, want %q (symbol: %q)", sym.File, path, sym.Name)
		}
	}

	// Verify all Symbol IDs are non-empty
	for _, sym := range symbols {
		if sym.ID == "" {
			t.Errorf("JavaScriptExtractor: Symbol.ID is empty for symbol %q", sym.Name)
		}
	}
}

// TestCSharpExtractor_BasicSymbols verifies that the C# extractor extracts
// class, interface, and method symbols from a small C# snippet.
func TestCSharpExtractor_BasicSymbols(t *testing.T) {
	src := []byte(`using System;

interface IShape {
    double Area();
}

class Circle : IShape {
    private double radius;

    public Circle(double radius) {
        this.radius = radius;
    }

    public double Area() {
        return Math.PI * radius * radius;
    }
}
`)
	const path = "testdata/shapes.cs"
	e := &CSharpExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("CSharpExtractor.Extract() error: %v", err)
	}

	hasClass := false
	hasInterface := false
	hasMethod := false
	for _, sym := range symbols {
		switch sym.Kind {
		case KindClass:
			hasClass = true
		case KindInterface:
			hasInterface = true
		case KindMethod:
			hasMethod = true
		}
	}
	if !hasClass {
		t.Errorf("CSharpExtractor: expected at least one class symbol, got: %+v", symbols)
	}
	if !hasInterface {
		t.Errorf("CSharpExtractor: expected at least one interface symbol, got: %+v", symbols)
	}
	if !hasMethod {
		t.Errorf("CSharpExtractor: expected at least one method symbol, got: %+v", symbols)
	}

	// Verify all Symbol.File fields equal the input path
	for _, sym := range symbols {
		if sym.File != path {
			t.Errorf("CSharpExtractor: Symbol.File = %q, want %q (symbol: %q)", sym.File, path, sym.Name)
		}
	}

	// Verify all Symbol IDs are non-empty
	for _, sym := range symbols {
		if sym.ID == "" {
			t.Errorf("CSharpExtractor: Symbol.ID is empty for symbol %q", sym.Name)
		}
	}
}

// ---- Task 4.11: Property tests -----------------------------------------------

// TestProperty_GenerateSymbolIDDeterministic verifies that for any
// (projectID, filePath, symbolName, kind) tuple, GenerateSymbolID always
// returns the same value when called twice.
//
// **Validates: Requirements 2.7, 2.8**
func TestProperty_GenerateSymbolIDDeterministic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		projectID := rapid.StringMatching(`[a-zA-Z0-9_-]{0,20}`).Draw(rt, "projectID")
		filePath := rapid.StringMatching(`[a-zA-Z0-9/_.-]{1,50}`).Draw(rt, "filePath")
		symbolName := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_.]{0,30}`).Draw(rt, "symbolName")
		kind := rapid.SampledFrom([]SymbolKind{
			KindFunction, KindMethod, KindType, KindInterface,
			KindVariable, KindModule, KindClass,
		}).Draw(rt, "kind")

		id1 := GenerateSymbolID(projectID, filePath, symbolName, kind, "")
		id2 := GenerateSymbolID(projectID, filePath, symbolName, kind, "")

		if id1 != id2 {
			rt.Fatalf(
				"GenerateSymbolID is not deterministic: first call returned %q, second returned %q "+
					"(projectID=%q, filePath=%q, symbolName=%q, kind=%q)",
				id1, id2, projectID, filePath, symbolName, kind,
			)
		}

		// Also verify the ID has the expected length (32 hex chars = 16 bytes)
		if len(id1) != 32 {
			rt.Fatalf("GenerateSymbolID returned ID of length %d, want 32 (got %q)", len(id1), id1)
		}
	})
}

// extractorTestCase bundles an extractor with a representative source snippet
// and the file path to use when calling Extract.
type extractorTestCase struct {
	name      string
	extractor Extractor
	path      string
	src       []byte
}

// allExtractorCases returns test cases for all five language extractors.
func allExtractorCases() []extractorTestCase {
	return []extractorTestCase{
		{
			name:      "go",
			extractor: &GoExtractor{},
			path:      "testdata/greeter.go",
			src: []byte(`package main

import "fmt"

type Greeter interface {
    Greet(name string) string
}

type SimpleGreeter struct{}

func (g *SimpleGreeter) Greet(name string) string {
    return fmt.Sprintf("Hello, %s!", name)
}

func main() {
    g := &SimpleGreeter{}
    g.Greet("World")
}
`),
		},
		{
			name:      "typescript",
			extractor: &TypeScriptExtractor{},
			path:      "testdata/animals.ts",
			src: []byte(`interface Animal {
    name: string;
}

class Dog implements Animal {
    name: string;
    constructor(name: string) { this.name = name; }
    bark(): void { console.log("Woof!"); }
}

function createDog(name: string): Dog {
    return new Dog(name);
}
`),
		},
		{
			name:      "python",
			extractor: &PythonExtractor{},
			path:      "testdata/calculator.py",
			src: []byte(`class Calculator:
    def add(self, a, b):
        return a + b

    def multiply(self, a, b):
        return a * b

def main():
    calc = Calculator()
    calc.add(1, 2)
`),
		},
		{
			name:      "javascript",
			extractor: &JavaScriptExtractor{},
			path:      "testdata/emitter.js",
			src: []byte(`class EventEmitter {
    constructor() {
        this.listeners = {};
    }

    on(event, callback) {
        this.listeners[event] = this.listeners[event] || [];
        this.listeners[event].push(callback);
    }
}

function createEmitter() {
    return new EventEmitter();
}
`),
		},
		{
			name:      "csharp",
			extractor: &CSharpExtractor{},
			path:      "testdata/shapes.cs",
			src: []byte(`using System;

interface IShape {
    double Area();
}

class Circle : IShape {
    private double radius;

    public Circle(double radius) {
        this.radius = radius;
    }

    public double Area() {
        return Math.PI * radius * radius;
    }
}
`),
		},
	}
}

// TestProperty_SymbolFileEqualsPath verifies that for any source file parsed by
// an extractor, all returned Symbol.File fields equal the input path.
//
// **Validates: Requirement 2.9**
func TestProperty_SymbolFileEqualsPath(t *testing.T) {
	for _, tc := range allExtractorCases() {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			rapid.Check(t, func(rt *rapid.T) {
				// Use a generated path to ensure the property holds for any path value.
				path := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9/_.-]{0,40}\.[a-z]{1,4}`).Draw(rt, "path")

				symbols, _, err := tc.extractor.Extract(path, tc.src)
				if isCGoError(err) {
					t.Skip("CGo not available")
				}
				if err != nil {
					rt.Fatalf("%s extractor.Extract() error: %v", tc.name, err)
				}

				for _, sym := range symbols {
					if sym.File != path {
						rt.Fatalf(
							"%s extractor: Symbol.File = %q, want %q (symbol: %q kind: %q)",
							tc.name, sym.File, path, sym.Name, sym.Kind,
						)
					}
				}
			})
		})
	}
}

// TestProperty_SymbolIDsUnique verifies that for any source file parsed by an
// extractor, all returned Symbol IDs are unique within the result set.
//
// **Validates: Requirement 2.10**
func TestProperty_SymbolIDsUnique(t *testing.T) {
	for _, tc := range allExtractorCases() {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			rapid.Check(t, func(rt *rapid.T) {
				symbols, _, err := tc.extractor.Extract(tc.path, tc.src)
				if isCGoError(err) {
					t.Skip("CGo not available")
				}
				if err != nil {
					rt.Fatalf("%s extractor.Extract() error: %v", tc.name, err)
				}

				seen := make(map[string]string, len(symbols))
				for _, sym := range symbols {
					if prev, exists := seen[sym.ID]; exists {
						rt.Fatalf(
							"%s extractor: duplicate Symbol ID %q for symbols %q and %q",
							tc.name, sym.ID, prev, sym.Name,
						)
					}
					seen[sym.ID] = sym.Name
				}
			})
		})
	}
}
