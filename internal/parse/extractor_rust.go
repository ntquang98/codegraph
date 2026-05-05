//go:build cgo

package parse

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/rust"
)

// RustExtractor extracts symbols and edges from Rust source files using tree-sitter.
type RustExtractor struct{}

func (e *RustExtractor) Language() string     { return "rust" }
func (e *RustExtractor) Extensions() []string { return []string{".rs"} }

// Extract parses a Rust source file and returns all symbols and edges found.
func (e *RustExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
	lang := rust.GetLanguage()
	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, nil, err
	}
	defer tree.Close()

	root := tree.RootNode()
	var symbols []Symbol
	var edges []Edge

	// Track symbol names → IDs for call resolution
	symbolsByName := make(map[string]string)

	// File module symbol — used as fallback FromID for imports and calls
	fileModuleID := GenerateSymbolID("", path, path, KindModule, "")

	// First pass: collect top-level declarations
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "function_item":
			sym := extractRustFunction(child, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "struct_item":
			sym := extractRustNamedItem(child, src, path, KindType)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "enum_item":
			sym := extractRustNamedItem(child, src, path, KindType)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "trait_item":
			sym := extractRustNamedItem(child, src, path, KindInterface)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "use_declaration":
			importSyms, importEdges := extractRustUseDeclaration(child, src, path, fileModuleID)
			for _, s := range importSyms {
				s := s
				symbols = append(symbols, s)
				symbolsByName[s.Name] = s.ID
			}
			edges = append(edges, importEdges...)
		case "impl_item":
			implSyms, implEdges := extractRustImplBlock(child, src, path)
			for _, s := range implSyms {
				s := s
				symbols = append(symbols, s)
				symbolsByName[s.Name] = s.ID
			}
			edges = append(edges, implEdges...)
		}
	}

	// Second pass: extract call edges from function/method bodies
	callEdges := extractRustCallEdges(root, src, path, symbolsByName, fileModuleID)
	edges = append(edges, callEdges...)

	return symbols, edges, nil
}

// extractRustFunction extracts a function_item node as a KindFunction symbol.
func extractRustFunction(node *sitter.Node, src []byte, path string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	id := GenerateSymbolID("", path, name, KindFunction, "")
	return &Symbol{
		ID:        id,
		Name:      name,
		Kind:      KindFunction,
		File:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: "fn " + name,
		ProjectID: "",
	}
}

// extractRustNamedItem extracts a named item (struct, enum, trait) by looking
// for the "name" field on the node.
func extractRustNamedItem(node *sitter.Node, src []byte, path string, kind SymbolKind) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	id := GenerateSymbolID("", path, name, kind, "")
	return &Symbol{
		ID:        id,
		Name:      name,
		Kind:      kind,
		File:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: name,
		ProjectID: "",
	}
}

// extractRustUseDeclaration extracts a use_declaration node and produces a
// KindModule symbol plus an EdgeImports from the file module symbol.
func extractRustUseDeclaration(node *sitter.Node, src []byte, path string, fileModuleID string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	// The use path is the text of the argument child (everything after "use " and before ";")
	// We collect the full text of the use_declaration, strip "use " prefix and ";" suffix.
	text := node.Content(src)
	text = strings.TrimPrefix(text, "use ")
	text = strings.TrimSuffix(text, ";")
	text = strings.TrimSpace(text)

	if text == "" {
		return syms, edges
	}

	modID := GenerateSymbolID("", text, text, KindModule, "")
	syms = append(syms, Symbol{
		ID:        modID,
		Name:      text,
		Kind:      KindModule,
		File:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
		Signature: text,
		ProjectID: "",
	})
	edges = append(edges, Edge{
		FromID: fileModuleID,
		ToID:   modID,
		Kind:   EdgeImports,
		File:   path,
		Line:   int(node.StartPoint().Row) + 1,
	})
	return syms, edges
}

// extractRustImplBlock extracts an impl_item node, producing KindMethod symbols
// for each function_item child and optionally an EdgeImplements edge for
// "impl Trait for Type" blocks.
func extractRustImplBlock(node *sitter.Node, src []byte, path string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	// Determine the type name being implemented.
	// For "impl TypeName { ... }" the "type" field holds the type.
	// For "impl Trait for Type { ... }" the "type" field holds the implementing type
	// and the "trait" field holds the trait.
	typeNode := node.ChildByFieldName("type")
	if typeNode == nil {
		return syms, edges
	}
	typeName := typeNode.Content(src)
	// Strip generic parameters: "Vec<T>" → "Vec"
	if idx := strings.IndexByte(typeName, '<'); idx >= 0 {
		typeName = typeName[:idx]
	}
	typeName = strings.TrimSpace(typeName)

	// Check for trait implementation: "impl Trait for Type"
	traitNode := node.ChildByFieldName("trait")
	if traitNode != nil {
		traitName := traitNode.Content(src)
		// Strip generic parameters from trait name
		if idx := strings.IndexByte(traitName, '<'); idx >= 0 {
			traitName = traitName[:idx]
		}
		traitName = strings.TrimSpace(traitName)

		typeSymID := GenerateSymbolID("", path, typeName, KindType, "")
		traitSymID := GenerateSymbolID("", path, traitName, KindInterface, "")
		edges = append(edges, Edge{
			FromID: typeSymID,
			ToID:   traitSymID,
			Kind:   EdgeImplements,
			File:   path,
			Line:   int(node.StartPoint().Row) + 1,
		})
	}

	// Walk the body of the impl block for function_item children
	bodyNode := node.ChildByFieldName("body")
	if bodyNode == nil {
		return syms, edges
	}

	for i := 0; i < int(bodyNode.ChildCount()); i++ {
		child := bodyNode.Child(i)
		if child.Type() != "function_item" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		methodName := nameNode.Content(src)
		qualifiedName := typeName + "." + methodName
		startLine := int(child.StartPoint().Row) + 1
		endLine := int(child.EndPoint().Row) + 1

		id := GenerateSymbolID("", path, qualifiedName, KindMethod, "")
		syms = append(syms, Symbol{
			ID:        id,
			Name:      qualifiedName,
			Kind:      KindMethod,
			File:      path,
			StartLine: startLine,
			EndLine:   endLine,
			Signature: "fn " + qualifiedName,
			ProjectID: "",
		})
	}

	return syms, edges
}

// extractRustCallEdges walks the entire AST looking for call_expression nodes
// and produces EdgeCalls edges. The enclosing function or method is used as
// FromID where resolvable; otherwise the file module symbol is used.
func extractRustCallEdges(root *sitter.Node, src []byte, path string, symbolsByName map[string]string, fileModuleID string) []Edge {
	var edges []Edge

	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Type() == "call_expression" {
			funcNode := node.ChildByFieldName("function")
			if funcNode != nil {
				calleeName := extractRustCalleeName(funcNode, src)
				if calleeName != "" {
					fromID := resolveRustEnclosingID(node, src, path, symbolsByName, fileModuleID)
					toID, ok := symbolsByName[calleeName]
					if !ok {
						toID = GenerateSymbolID("", path, calleeName, KindFunction, "")
					}
					edges = append(edges, Edge{
						FromID: fromID,
						ToID:   toID,
						Kind:   EdgeCalls,
						File:   path,
						Line:   int(node.StartPoint().Row) + 1,
					})
				}
			}
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)
	return edges
}

// resolveRustEnclosingID walks up the AST from node to find the nearest
// enclosing function_item or impl method, returning its symbol ID.
// Falls back to fileModuleID if no enclosing function is found.
func resolveRustEnclosingID(node *sitter.Node, src []byte, path string, symbolsByName map[string]string, fileModuleID string) string {
	cur := node.Parent()
	for cur != nil {
		if cur.Type() == "function_item" {
			nameNode := cur.ChildByFieldName("name")
			if nameNode != nil {
				name := nameNode.Content(src)
				// Check if this function is inside an impl block
				parent := cur.Parent()
				if parent != nil && parent.Type() == "declaration_list" {
					implNode := parent.Parent()
					if implNode != nil && implNode.Type() == "impl_item" {
						typeNode := implNode.ChildByFieldName("type")
						if typeNode != nil {
							typeName := typeNode.Content(src)
							if idx := strings.IndexByte(typeName, '<'); idx >= 0 {
								typeName = typeName[:idx]
							}
							typeName = strings.TrimSpace(typeName)
							qualifiedName := typeName + "." + name
							if id, ok := symbolsByName[qualifiedName]; ok {
								return id
							}
						}
					}
				}
				// Top-level function
				if id, ok := symbolsByName[name]; ok {
					return id
				}
			}
		}
		cur = cur.Parent()
	}
	return fileModuleID
}

// extractRustCalleeName extracts the callee name from a function node in a
// call_expression.
func extractRustCalleeName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "identifier":
		return node.Content(src)
	case "scoped_identifier":
		// e.g. module::function — use the last segment
		nameNode := node.ChildByFieldName("name")
		if nameNode != nil {
			return nameNode.Content(src)
		}
		// Fallback: take the last "::" segment
		text := node.Content(src)
		parts := strings.Split(text, "::")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	case "field_expression":
		// e.g. self.method or obj.method
		fieldNode := node.ChildByFieldName("field")
		if fieldNode != nil {
			return fieldNode.Content(src)
		}
	}
	return ""
}
