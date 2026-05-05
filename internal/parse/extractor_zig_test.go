//go:build cgo

package parse

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// ---- Task 4.5: Unit tests for ZigExtractor -----------------------------------

// TestZigExtractor_BasicSymbols parses a small Zig snippet and verifies that
// function, type, and method symbols are extracted, all Symbol.File fields equal
// the path, and all IDs are non-empty.
//
// Requirements: 2.2, 2.3, 2.4, 2.5, 2.8, 2.9
func TestZigExtractor_BasicSymbols(t *testing.T) {
	src := []byte(`
const std = @import("std");

pub fn add(a: i32, b: i32) i32 {
    return a + b;
}

pub fn subtract(a: i32, b: i32) i32 {
    return a - b;
}

const Point = struct {
    x: f64 = 0,
    y: f64 = 0,

    pub fn init(x: f64, y: f64) Point {
        return Point{ .x = x, .y = y };
    }

    pub fn distance(self: @This(), other: Point) f64 {
        const dx = self.x - other.x;
        const dy = self.y - other.y;
        return dx * dx + dy * dy;
    }
};

const Direction = enum {
    North,
    South,
    East,
    West,
};
`)
	const path = "testdata/basic.zig"
	e := &ZigExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("ZigExtractor.Extract() error: %v", err)
	}

	// Verify expected kinds are present
	hasFunction := false
	hasMethod := false
	hasType := false
	for _, sym := range symbols {
		switch sym.Kind {
		case KindFunction:
			hasFunction = true
		case KindMethod:
			hasMethod = true
		case KindType:
			hasType = true
		}
	}
	if !hasFunction {
		t.Errorf("ZigExtractor: expected at least one KindFunction symbol, got: %+v", symbols)
	}
	if !hasMethod {
		t.Errorf("ZigExtractor: expected at least one KindMethod symbol, got: %+v", symbols)
	}
	if !hasType {
		t.Errorf("ZigExtractor: expected at least one KindType symbol, got: %+v", symbols)
	}

	// Verify all Symbol.File fields equal the input path
	for _, sym := range symbols {
		if sym.File != path {
			t.Errorf("ZigExtractor: Symbol.File = %q, want %q (symbol: %q)", sym.File, path, sym.Name)
		}
	}

	// Verify all Symbol IDs are non-empty
	for _, sym := range symbols {
		if sym.ID == "" {
			t.Errorf("ZigExtractor: Symbol.ID is empty for symbol %q", sym.Name)
		}
	}
}

// TestZigExtractor_StructMethods parses a snippet with struct methods and verifies
// that method symbols are qualified as StructName.fn_name.
//
// Requirements: 2.5
func TestZigExtractor_StructMethods(t *testing.T) {
	src := []byte(`
const Counter = struct {
    value: u32 = 0,

    pub fn init() Counter {
        return Counter{ .value = 0 };
    }

    pub fn increment(self: *@This()) void {
        self.value += 1;
    }

    pub fn get(self: @This()) u32 {
        return self.value;
    }
};
`)
	const path = "testdata/counter.zig"
	e := &ZigExtractor{}
	symbols, _, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("ZigExtractor.Extract() error: %v", err)
	}

	// Collect method names
	methodNames := make(map[string]bool)
	for _, sym := range symbols {
		if sym.Kind == KindMethod {
			methodNames[sym.Name] = true
		}
	}

	// Verify qualified method names
	expectedMethods := []string{"Counter.init", "Counter.increment", "Counter.get"}
	for _, want := range expectedMethods {
		if !methodNames[want] {
			t.Errorf("ZigExtractor: expected method %q, got methods: %v", want, methodNames)
		}
	}
}

// TestZigExtractor_ImportBuiltin parses a snippet with @import calls and verifies
// that KindModule symbols and EdgeImports edges are produced.
//
// Requirements: 2.6
func TestZigExtractor_ImportBuiltin(t *testing.T) {
	src := []byte(`
const std = @import("std");
const mem = @import("mem");

pub fn greet(name: []const u8) void {
    _ = name;
}
`)
	const path = "testdata/imports.zig"
	e := &ZigExtractor{}
	symbols, edges, err := e.Extract(path, src)
	if isCGoError(err) {
		t.Skip("CGo not available")
	}
	if err != nil {
		t.Fatalf("ZigExtractor.Extract() error: %v", err)
	}

	// Verify KindModule symbols are produced for @import calls
	moduleNames := make(map[string]bool)
	for _, sym := range symbols {
		if sym.Kind == KindModule {
			moduleNames[sym.Name] = true
		}
	}
	if len(moduleNames) == 0 {
		t.Errorf("ZigExtractor: expected KindModule symbols for @import calls, got symbols: %+v", symbols)
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
		t.Errorf("ZigExtractor: expected at least one EdgeImports edge for @import calls, got edges: %+v", edges)
	}
}

// ---- Task 4.6: Property test: Zig Symbol.File equals input path --------------

// TestProperty_ZigSymbolFileEqualsPath verifies that for any file path and any
// Zig source content, all Symbols returned by ZigExtractor.Extract(path, src)
// have their File field equal to path.
//
// **Validates: Requirements 2.8, 5.6**
func TestProperty_ZigSymbolFileEqualsPath(t *testing.T) {
	src := []byte(`
const std = @import("std");

pub fn topLevel() i32 {
    return 42;
}

const MyStruct = struct {
    value: i32 = 0,

    pub fn init(v: i32) MyStruct {
        return MyStruct{ .value = v };
    }
};

const MyEnum = enum {
    VariantA,
    VariantB,
};
`)

	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random file path
		path := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9/_.-]{0,40}\.zig`).Draw(rt, "path")

		e := &ZigExtractor{}
		symbols, _, err := e.Extract(path, src)
		if isCGoError(err) {
			t.Skip("CGo not available")
		}
		if err != nil {
			rt.Fatalf("ZigExtractor.Extract() error: %v", err)
		}

		for _, sym := range symbols {
			if sym.File != path {
				rt.Fatalf(
					"ZigExtractor: Symbol.File = %q, want %q (symbol: %q kind: %q)",
					sym.File, path, sym.Name, sym.Kind,
				)
			}
		}
	})
}

// ---- Task 4.7: Property test: Zig Symbol IDs are unique within a file --------

// TestProperty_ZigSymbolIDsUnique verifies that for any Zig source file parsed
// by ZigExtractor, all returned Symbol IDs within the result set are distinct.
//
// **Validates: Requirements 2.9, 5.8**
func TestProperty_ZigSymbolIDsUnique(t *testing.T) {
	src := []byte(`
const std = @import("std");

pub fn createCounter() Counter {
    return Counter{ .value = 0 };
}

pub fn processItems(count: u32) void {
    _ = count;
}

const Counter = struct {
    value: u32 = 0,

    pub fn init() Counter {
        return Counter{ .value = 0 };
    }

    pub fn increment(self: *@This()) void {
        self.value += 1;
    }

    pub fn get(self: @This()) u32 {
        return self.value;
    }
};

const Status = enum {
    Active,
    Inactive,
    Pending,
};
`)

	rapid.Check(t, func(rt *rapid.T) {
		e := &ZigExtractor{}
		symbols, _, err := e.Extract("testdata/registry.zig", src)
		if isCGoError(err) {
			t.Skip("CGo not available")
		}
		if err != nil {
			rt.Fatalf("ZigExtractor.Extract() error: %v", err)
		}

		seen := make(map[string]string, len(symbols))
		for _, sym := range symbols {
			if prev, exists := seen[sym.ID]; exists {
				rt.Fatalf(
					"ZigExtractor: duplicate Symbol ID %q for symbols %q and %q",
					sym.ID, prev, sym.Name,
				)
			}
			seen[sym.ID] = sym.Name
		}
	})
}

// ---- Task 4.8: Property test: Zig function extraction completeness -----------

// TestProperty_ZigFunctionExtraction verifies that for any set of top-level fn
// declarations in a Zig source file, ZigExtractor produces a KindFunction symbol
// for each declared function with the correct name.
//
// **Validates: Requirements 2.2**
func TestProperty_ZigFunctionExtraction(t *testing.T) {
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

		// Build Zig source with those function declarations
		var sb strings.Builder
		for _, name := range names {
			sb.WriteString(fmt.Sprintf("pub fn %s() i32 { return 0; }\n", name))
		}
		src := []byte(sb.String())

		e := &ZigExtractor{}
		symbols, _, err := e.Extract("testdata/funcs.zig", src)
		if isCGoError(err) {
			t.Skip("CGo not available")
		}
		if err != nil {
			rt.Fatalf("ZigExtractor.Extract() error: %v", err)
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
					"ZigExtractor: expected KindFunction symbol for %q, got function symbols: %v",
					name, funcNames,
				)
			}
		}
	})
}

// ---- Task 4.9: Property test: Zig type extraction ----------------------------

// TestProperty_ZigTypeExtraction verifies that for any Zig source file containing
// const Name = struct { ... } or const Name = enum { ... } declarations,
// ZigExtractor produces KindType symbols for each named struct and enum type.
//
// **Validates: Requirements 2.3, 2.4**
func TestProperty_ZigTypeExtraction(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate unique names for structs and enums
		structName := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "struct_name")
		enumName := rapid.StringMatching(`[A-Z][a-zA-Z0-9]{0,15}`).Draw(rt, "enum_name")

		// Ensure names are distinct to avoid ID collisions
		if structName == enumName {
			t.Skip("generated duplicate names, skipping")
		}

		// Build Zig source with struct and enum declarations
		src := []byte(fmt.Sprintf(`
const %s = struct {
    value: i32 = 0,
};

const %s = enum {
    VariantA,
    VariantB,
};
`, structName, enumName))

		e := &ZigExtractor{}
		symbols, _, err := e.Extract("testdata/types_test.zig", src)
		if isCGoError(err) {
			t.Skip("CGo not available")
		}
		if err != nil {
			rt.Fatalf("ZigExtractor.Extract() error: %v", err)
		}

		// Index KindType symbols by name
		typeSyms := make(map[string]bool)
		for _, sym := range symbols {
			if sym.Kind == KindType {
				typeSyms[sym.Name] = true
			}
		}

		// Verify struct → KindType
		if !typeSyms[structName] {
			rt.Fatalf(
				"ZigExtractor: expected KindType symbol for struct %q, got type symbols: %v",
				structName, typeSyms,
			)
		}

		// Verify enum → KindType
		if !typeSyms[enumName] {
			rt.Fatalf(
				"ZigExtractor: expected KindType symbol for enum %q, got type symbols: %v",
				enumName, typeSyms,
			)
		}
	})
}

// ---- Task 8.5: Fixture-based tests for ZigExtractor -------------------------

// TestZigExtractor_Fixtures parses the testdata/zig-sample/ files and verifies
// expected symbol/edge counts and specific named symbols.
//
// Requirements: 5.2, 5.4
func TestZigExtractor_Fixtures(t *testing.T) {
	type fixtureCase struct {
		file        string
		minSymbols  int
		minEdges    int
		namedSymbols []string // specific symbol names that must be present
	}

	cases := []fixtureCase{
		{
			file:       "../../../testdata/zig-sample/models.zig",
			minSymbols: 6,
			minEdges:   0,
			namedSymbols: []string{
				"User",
				"Product",
				"Order",
				"UserStatus",
				"OrderStatus",
				"User.init",
			},
		},
		{
			file:       "../../../testdata/zig-sample/service.zig",
			minSymbols: 5,
			minEdges:   5,
			namedSymbols: []string{
				"checkStock",
				"calculateTotal",
				"validateOrder",
				"placeOrder",
				"formatSummary",
			},
		},
	}

	e := &ZigExtractor{}

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
				t.Fatalf("ZigExtractor.Extract(%q) error: %v", tc.file, err)
			}

			if len(symbols) < tc.minSymbols {
				t.Errorf("ZigExtractor: got %d symbols, want >= %d (file: %s)", len(symbols), tc.minSymbols, tc.file)
			}
			if len(edges) < tc.minEdges {
				t.Errorf("ZigExtractor: got %d edges, want >= %d (file: %s)", len(edges), tc.minEdges, tc.file)
			}

			// Build a set of symbol names for fast lookup
			symNames := make(map[string]bool, len(symbols))
			for _, s := range symbols {
				symNames[s.Name] = true
			}

			for _, want := range tc.namedSymbols {
				if !symNames[want] {
					t.Errorf("ZigExtractor: expected symbol %q not found in %s; got: %v", want, tc.file, symNames)
				}
			}
		})
	}
}
