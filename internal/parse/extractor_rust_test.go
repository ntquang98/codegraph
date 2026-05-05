//go:build cgo

package parse

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// ---- Task 3.5: Unit tests for RustExtractor ----------------------------------

// TestRustExtractor_BasicSymbols parses a small Rust snippet and verifies that
// function, method, type, and interface symbols are extracted, all Symbol.File
// fields equal the path, and all IDs are non-empty.
//
// Requirements: 1.2, 1.3, 1.4, 1.5, 1.9, 1.10
func TestRustExtractor_BasicSymbols(t *testing.T) {
	src := []byte(`
use std::fmt;

pub struct Point {
    x: f64,
    y: f64,
}

pub enum Direction {
    North,
    South,
    East,
    West,
}

pub trait Shape {
    fn area(&self) -> f64;
    fn perimeter(&self) -> f64;
}

impl Point {
    pub fn new(x: f64, y: f64) -> Self {
        Point { x, y }
    }

    pub fn distance(&self, other: &Point) -> f64 {
        let dx = self.x - other.x;
        let dy = self.y - other.y;
        (dx * dx + dy * dy).sqrt()
    }
}

pub fn origin() -> Point {
    Point::new(0.0, 0.0)
}
`)
	const path = "testdata/basic.rs"
	e := &RustExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("RustExtractor.Extract() error: %v", err)
	}

	// Verify expected kinds are present
	hasFunction := false
	hasMethod := false
	hasType := false
	hasInterface := false
	for _, sym := range symbols {
		switch sym.Kind {
		case KindFunction:
			hasFunction = true
		case KindMethod:
			hasMethod = true
		case KindType:
			hasType = true
		case KindInterface:
			hasInterface = true
		}
	}
	if !hasFunction {
		t.Errorf("RustExtractor: expected at least one KindFunction symbol, got: %+v", symbols)
	}
	if !hasMethod {
		t.Errorf("RustExtractor: expected at least one KindMethod symbol, got: %+v", symbols)
	}
	if !hasType {
		t.Errorf("RustExtractor: expected at least one KindType symbol, got: %+v", symbols)
	}
	if !hasInterface {
		t.Errorf("RustExtractor: expected at least one KindInterface symbol, got: %+v", symbols)
	}

	// Verify all Symbol.File fields equal the input path
	for _, sym := range symbols {
		if sym.File != path {
			t.Errorf("RustExtractor: Symbol.File = %q, want %q (symbol: %q)", sym.File, path, sym.Name)
		}
	}

	// Verify all Symbol IDs are non-empty
	for _, sym := range symbols {
		if sym.ID == "" {
			t.Errorf("RustExtractor: Symbol.ID is empty for symbol %q", sym.Name)
		}
	}
}

// TestRustExtractor_ImplBlock parses a snippet with impl blocks and verifies
// that method symbols are qualified as TypeName.method_name.
//
// Requirements: 1.3
func TestRustExtractor_ImplBlock(t *testing.T) {
	src := []byte(`
pub struct Counter {
    value: u32,
}

impl Counter {
    pub fn new() -> Self {
        Counter { value: 0 }
    }

    pub fn increment(&mut self) {
        self.value += 1;
    }

    pub fn get(&self) -> u32 {
        self.value
    }
}
`)
	const path = "testdata/counter.rs"
	e := &RustExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("RustExtractor.Extract() error: %v", err)
	}

	// Collect method names
	methodNames := make(map[string]bool)
	for _, sym := range symbols {
		if sym.Kind == KindMethod {
			methodNames[sym.Name] = true
		}
	}

	// Verify qualified method names
	expectedMethods := []string{"Counter.new", "Counter.increment", "Counter.get"}
	for _, want := range expectedMethods {
		if !methodNames[want] {
			t.Errorf("RustExtractor: expected method %q, got methods: %v", want, methodNames)
		}
	}
}

// TestRustExtractor_TraitImpl parses a snippet with impl Trait for Type and
// verifies that an EdgeImplements edge is produced.
//
// Requirements: 1.8
func TestRustExtractor_TraitImpl(t *testing.T) {
	src := []byte(`
pub trait Drawable {
    fn draw(&self);
}

pub struct Circle {
    radius: f64,
}

impl Drawable for Circle {
    fn draw(&self) {
        println!("Drawing circle with radius {}", self.radius);
    }
}
`)
	const path = "testdata/drawable.rs"
	e := &RustExtractor{}
	_, edges, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("RustExtractor.Extract() error: %v", err)
	}

	// Verify at least one EdgeImplements edge is produced
	hasImplements := false
	for _, edge := range edges {
		if edge.Kind == EdgeImplements {
			hasImplements = true
			break
		}
	}
	if !hasImplements {
		t.Errorf("RustExtractor: expected at least one EdgeImplements edge for 'impl Drawable for Circle', got edges: %+v", edges)
	}
}

// TestRustExtractor_UseDeclarations parses a snippet with use declarations and
// verifies that KindModule symbols and EdgeImports edges are produced.
//
// Requirements: 1.6
func TestRustExtractor_UseDeclarations(t *testing.T) {
	src := []byte(`
use std::collections::HashMap;
use std::fmt::Display;

pub fn greet(name: &str) {
    println!("Hello, {}!", name);
}
`)
	const path = "testdata/imports.rs"
	e := &RustExtractor{}
	symbols, edges, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("RustExtractor.Extract() error: %v", err)
	}

	// Verify KindModule symbols are produced for use declarations
	moduleNames := make(map[string]bool)
	for _, sym := range symbols {
		if sym.Kind == KindModule {
			moduleNames[sym.Name] = true
		}
	}
	if len(moduleNames) == 0 {
		t.Errorf("RustExtractor: expected KindModule symbols for use declarations, got symbols: %+v", symbols)
	}

	// Verify EdgeImports edges are produced
	hasImports := false
	for _, edge := range edges {
		if edge.Kind == EdgeImports {
			hasImports = true
			break
		}
	}
	if !hasImports {
		t.Errorf("RustExtractor: expected at least one EdgeImports edge for use declarations, got edges: %+v", edges)
	}
}

// ---- Task 3.6: Property test: Rust Symbol.File equals input path -------------

// TestProperty_RustSymbolFileEqualsPath verifies that for any file path and any
// Rust source content, all Symbols returned by RustExtractor.Extract(path, src)
// have their File field equal to path.
//
// **Validates: Requirements 1.9, 5.5**
func TestProperty_RustSymbolFileEqualsPath(t *testing.T) {
	src := []byte(`
use std::fmt;

pub struct Foo {
    x: i32,
}

pub trait Bar {
    fn baz(&self) -> i32;
}

impl Foo {
    pub fn new(x: i32) -> Self {
        Foo { x }
    }
}

pub fn top_level() -> i32 {
    42
}
`)

	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random file path
		path := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9/_.-]{0,40}\.rs`).Draw(rt, "path")

		e := &RustExtractor{}
		symbols, _, err := e.Extract(path, src)
		if isCGoError(err) {
			t.Skip("CGo not available")
		}
		if err != nil {
			rt.Fatalf("RustExtractor.Extract() error: %v", err)
		}

		for _, sym := range symbols {
			if sym.File != path {
				rt.Fatalf(
					"RustExtractor: Symbol.File = %q, want %q (symbol: %q kind: %q)",
					sym.File, path, sym.Name, sym.Kind,
				)
			}
		}
	})
}

// ---- Task 3.7: Property test: Rust Symbol IDs are unique within a file -------

// TestProperty_RustSymbolIDsUnique verifies that for any Rust source file
// parsed by RustExtractor, all returned Symbol IDs within the result set are
// distinct.
//
// **Validates: Requirements 1.10, 5.7**
func TestProperty_RustSymbolIDsUnique(t *testing.T) {
	src := []byte(`
use std::collections::HashMap;

pub struct Registry {
    items: HashMap<String, i32>,
}

pub enum Status {
    Active,
    Inactive,
    Pending,
}

pub trait Processor {
    fn process(&self, input: &str) -> String;
}

impl Registry {
    pub fn new() -> Self {
        Registry { items: HashMap::new() }
    }

    pub fn insert(&mut self, key: String, value: i32) {
        self.items.insert(key, value);
    }

    pub fn get(&self, key: &str) -> Option<&i32> {
        self.items.get(key)
    }
}

pub fn create_registry() -> Registry {
    Registry::new()
}
`)

	rapid.Check(t, func(rt *rapid.T) {
		e := &RustExtractor{}
		symbols, _, err := e.Extract("testdata/registry.rs", src)
		if isCGoError(err) {
			t.Skip("CGo not available")
		}
		if err != nil {
			rt.Fatalf("RustExtractor.Extract() error: %v", err)
		}

		seen := make(map[string]string, len(symbols))
		for _, sym := range symbols {
			if prev, exists := seen[sym.ID]; exists {
				rt.Fatalf(
					"RustExtractor: duplicate Symbol ID %q for symbols %q and %q",
					sym.ID, prev, sym.Name,
				)
			}
			seen[sym.ID] = sym.Name
		}
	})
}

// ---- Task 3.8: Property test: Rust function extraction completeness ----------

// TestProperty_RustFunctionExtraction verifies that for any set of top-level fn
// declarations in a Rust source file, RustExtractor produces a KindFunction
// symbol for each declared function with the correct name.
//
// **Validates: Requirements 1.2**
func TestProperty_RustFunctionExtraction(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate between 1 and 5 unique function names
		count := rapid.IntRange(1, 5).Draw(rt, "count")
		names := make([]string, 0, count)
		seen := make(map[string]bool)
		for len(names) < count {
			name := rapid.StringMatching(`[a-z][a-z0-9_]{0,15}`).Draw(rt, fmt.Sprintf("fn_name_%d", len(names)))
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}

		// Build Rust source with those function declarations
		var sb strings.Builder
		for _, name := range names {
			sb.WriteString(fmt.Sprintf("pub fn %s() -> i32 { 0 }\n", name))
		}
		src := []byte(sb.String())

		e := &RustExtractor{}
		symbols, _, err := e.Extract("testdata/funcs.rs", src)
		if isCGoError(err) {
			t.Skip("CGo not available")
		}
		if err != nil {
			rt.Fatalf("RustExtractor.Extract() error: %v", err)
		}

		// Collect all KindFunction symbol names
		funcNames := make(map[string]bool)
		for _, sym := range symbols {
			if sym.Kind == KindFunction {
				funcNames[sym.Name] = true
			}
		}

		// Verify every generated function name appears as a KindFunction symbol
		for _, name := range names {
			if !funcNames[name] {
				rt.Fatalf(
					"RustExtractor: expected KindFunction symbol for %q, got function symbols: %v",
					name, funcNames,
				)
			}
		}
	})
}

// ---- Task 3.9: Property test: Rust method qualification ----------------------

// TestProperty_RustMethodQualification verifies that for any impl TypeName block
// containing fn declarations, RustExtractor produces KindMethod symbols with
// names qualified as TypeName.method_name.
//
// **Validates: Requirements 1.3**
func TestProperty_RustMethodQualification(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a type name (must start with uppercase for Rust convention,
		// but the extractor doesn't enforce this — use any valid identifier)
		typeName := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "type_name")

		// Generate between 1 and 4 unique method names
		count := rapid.IntRange(1, 4).Draw(rt, "method_count")
		methodNames := make([]string, 0, count)
		seenMethods := make(map[string]bool)
		for len(methodNames) < count {
			mName := rapid.StringMatching(`[a-z][a-z0-9_]{0,15}`).Draw(rt, fmt.Sprintf("method_%d", len(methodNames)))
			if !seenMethods[mName] {
				seenMethods[mName] = true
				methodNames = append(methodNames, mName)
			}
		}

		// Build Rust source with a struct and impl block
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("pub struct %s {}\n\n", typeName))
		sb.WriteString(fmt.Sprintf("impl %s {\n", typeName))
		for _, mName := range methodNames {
			sb.WriteString(fmt.Sprintf("    pub fn %s(&self) {}\n", mName))
		}
		sb.WriteString("}\n")
		src := []byte(sb.String())

		e := &RustExtractor{}
		symbols, _, err := e.Extract("testdata/impl_test.rs", src)
		if isCGoError(err) {
			t.Skip("CGo not available")
		}
		if err != nil {
			rt.Fatalf("RustExtractor.Extract() error: %v", err)
		}

		// Collect all KindMethod symbol names
		methodSymNames := make(map[string]bool)
		for _, sym := range symbols {
			if sym.Kind == KindMethod {
				methodSymNames[sym.Name] = true
			}
		}

		// Verify every method appears as TypeName.method_name
		for _, mName := range methodNames {
			qualified := typeName + "." + mName
			if !methodSymNames[qualified] {
				rt.Fatalf(
					"RustExtractor: expected KindMethod symbol %q, got method symbols: %v",
					qualified, methodSymNames,
				)
			}
		}
	})
}

// ---- Task 3.10: Property test: Rust type and trait extraction ----------------

// TestProperty_RustTypeAndTraitExtraction verifies that for any Rust source file
// containing struct, enum, and trait definitions, RustExtractor produces
// KindType symbols for structs and enums, and KindInterface symbols for traits.
//
// **Validates: Requirements 1.4, 1.5**
func TestProperty_RustTypeAndTraitExtraction(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate unique names for structs, enums, and traits
		structName := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "struct_name")
		enumName := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "enum_name")
		traitName := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "trait_name")

		// Ensure all names are distinct to avoid ID collisions
		if structName == enumName || structName == traitName || enumName == traitName {
			// Skip this combination — rapid will try another
			t.Skip("generated duplicate names, skipping")
		}

		// Build Rust source
		src := []byte(fmt.Sprintf(`
pub struct %s {
    value: i32,
}

pub enum %s {
    VariantA,
    VariantB,
}

pub trait %s {
    fn do_something(&self);
}
`, structName, enumName, traitName))

		e := &RustExtractor{}
		symbols, _, err := e.Extract("testdata/types_test.rs", src)
		if isCGoError(err) {
			t.Skip("CGo not available")
		}
		if err != nil {
			rt.Fatalf("RustExtractor.Extract() error: %v", err)
		}

		// Index symbols by name and kind
		typeSyms := make(map[string]bool)
		interfaceSyms := make(map[string]bool)
		for _, sym := range symbols {
			switch sym.Kind {
			case KindType:
				typeSyms[sym.Name] = true
			case KindInterface:
				interfaceSyms[sym.Name] = true
			}
		}

		// Verify struct → KindType
		if !typeSyms[structName] {
			rt.Fatalf(
				"RustExtractor: expected KindType symbol for struct %q, got type symbols: %v",
				structName, typeSyms,
			)
		}

		// Verify enum → KindType
		if !typeSyms[enumName] {
			rt.Fatalf(
				"RustExtractor: expected KindType symbol for enum %q, got type symbols: %v",
				enumName, typeSyms,
			)
		}

		// Verify trait → KindInterface
		if !interfaceSyms[traitName] {
			rt.Fatalf(
				"RustExtractor: expected KindInterface symbol for trait %q, got interface symbols: %v",
				traitName, interfaceSyms,
			)
		}
	})
}

// ---- Task 8.5: Fixture-based tests for RustExtractor ------------------------

// TestRustExtractor_Fixtures parses the testdata/rust-sample/ files and verifies
// expected symbol/edge counts and specific named symbols.
//
// Requirements: 5.1, 5.3
func TestRustExtractor_Fixtures(t *testing.T) {
	type fixtureCase struct {
		file        string
		minSymbols  int
		minEdges    int
		namedSymbols []string // specific symbol names that must be present
	}

	cases := []fixtureCase{
		{
			file:       "../../../testdata/rust-sample/models.rs",
			minSymbols: 10,
			minEdges:   0,
			namedSymbols: []string{
				"User",
				"Product",
				"Order",
				"UserStatus",
				"OrderStatus",
				"Repository",
				"Logger",
				"Notifier",
				"User.new",
				"User.is_active",
			},
		},
		{
			file:       "../../../testdata/rust-sample/service.rs",
			minSymbols: 8,
			minEdges:   8,
			namedSymbols: []string{
				"check_stock",
				"build_order",
				"calculate_total",
				"validate_order",
				"format_order_summary",
				"OrderService.new",
				"OrderService.place_order",
				"OrderService.cancel_order",
			},
		},
	}

	e := &RustExtractor{}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.file, func(t *testing.T) {
			src, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatalf("os.ReadFile(%q) error: %v", tc.file, err)
			}

			symbols, edges, err := e.Extract(tc.file, src)
			if isCGoError(err) {
				t.Skip("CGo not available")
			}
			if err != nil {
				t.Fatalf("RustExtractor.Extract(%q) error: %v", tc.file, err)
			}

			if len(symbols) < tc.minSymbols {
				t.Errorf("RustExtractor: got %d symbols, want >= %d (file: %s)", len(symbols), tc.minSymbols, tc.file)
			}
			if len(edges) < tc.minEdges {
				t.Errorf("RustExtractor: got %d edges, want >= %d (file: %s)", len(edges), tc.minEdges, tc.file)
			}

			// Build a set of symbol names for fast lookup
			symNames := make(map[string]bool, len(symbols))
			for _, s := range symbols {
				symNames[s.Name] = true
			}

			for _, want := range tc.namedSymbols {
				if !symNames[want] {
					t.Errorf("RustExtractor: expected symbol %q not found in %s; got: %v", want, tc.file, symNames)
				}
			}
		})
	}
}
