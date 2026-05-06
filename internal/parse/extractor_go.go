//go:build cgo

package parse

import (
	"context"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

// GoExtractor extracts symbols and edges from Go source files using tree-sitter.
type GoExtractor struct {
	parserPool sync.Pool
}

func (e *GoExtractor) Language() string     { return "go" }
func (e *GoExtractor) Extensions() []string { return []string{".go"} }

func (e *GoExtractor) getParser() *sitter.Parser {
	if p, ok := e.parserPool.Get().(*sitter.Parser); ok {
		return p
	}
	p := sitter.NewParser()
	p.SetLanguage(golang.GetLanguage())
	return p
}

func (e *GoExtractor) putParser(p *sitter.Parser) {
	e.parserPool.Put(p)
}

// Extract parses a Go source file and returns all symbols and edges found.
func (e *GoExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
	parser := e.getParser()
	defer e.putParser(parser)

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

	// First pass: collect all top-level declarations
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		switch child.Type() {
		case "function_declaration":
			sym := extractGoFunction(child, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "method_declaration":
			sym := extractGoMethod(child, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "type_declaration":
			syms := extractGoTypeDecl(child, src, path)
			for _, sym := range syms {
				s := sym
				symbols = append(symbols, s)
				symbolsByName[s.Name] = s.ID
			}
		case "var_declaration", "const_declaration":
			syms := extractGoVarConst(child, src, path)
			for _, sym := range syms {
				s := sym
				symbols = append(symbols, s)
				symbolsByName[s.Name] = s.ID
			}
		case "import_declaration":
			importSyms, importEdges := extractGoImports(child, src, path)
			symbols = append(symbols, importSyms...)
			edges = append(edges, importEdges...)
		}
	}

	// Second pass: extract call edges from function/method bodies.
	// Build a node-ID → enclosing-symbol-ID map in a single top-down pass
	// so call resolution is O(n) instead of O(calls × depth).
	callEdges := extractGoCallEdges(root, src, path, symbolsByName)
	edges = append(edges, callEdges...)

	return symbols, edges, nil
}

// extractGoFunction extracts a function_declaration node.
func extractGoFunction(node *sitter.Node, src []byte, path string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	sig := buildGoFuncSignature(node, src)

	id := GenerateSymbolID("", path, name, KindFunction, "")
	return &Symbol{
		ID:        id,
		Name:      name,
		Kind:      KindFunction,
		File:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: sig,
		ProjectID: "",
	}
}

// extractGoMethod extracts a method_declaration node.
func extractGoMethod(node *sitter.Node, src []byte, path string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)

	// Include receiver type in the name for uniqueness
	receiverNode := node.ChildByFieldName("receiver")
	qualifiedName := name
	if receiverNode != nil {
		receiverText := receiverNode.Content(src)
		// Strip parens and pointer: "(r *Foo)" → "Foo"
		receiverText = strings.Trim(receiverText, "()")
		parts := strings.Fields(receiverText)
		if len(parts) >= 2 {
			typeName := strings.TrimPrefix(parts[len(parts)-1], "*")
			qualifiedName = typeName + "." + name
		}
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1
	sig := buildGoMethodSignature(node, src)

	id := GenerateSymbolID("", path, qualifiedName, KindMethod, "")
	return &Symbol{
		ID:        id,
		Name:      qualifiedName,
		Kind:      KindMethod,
		File:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: sig,
		ProjectID: "",
	}
}

// extractGoTypeDecl extracts type_declaration nodes (types and interfaces).
func extractGoTypeDecl(node *sitter.Node, src []byte, path string) []Symbol {
	var syms []Symbol
	for i := 0; i < int(node.ChildCount()); i++ {
		spec := node.Child(i)
		if spec.Type() != "type_spec" {
			continue
		}
		nameNode := spec.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		name := nameNode.Content(src)
		startLine := int(spec.StartPoint().Row) + 1
		endLine := int(spec.EndPoint().Row) + 1

		// Determine if it's an interface or a regular type
		kind := KindType
		typeNode := spec.ChildByFieldName("type")
		if typeNode != nil && typeNode.Type() == "interface_type" {
			kind = KindInterface
		}

		id := GenerateSymbolID("", path, name, kind, "")
		syms = append(syms, Symbol{
			ID:        id,
			Name:      name,
			Kind:      kind,
			File:      path,
			StartLine: startLine,
			EndLine:   endLine,
			Signature: name,
			ProjectID: "",
		})
	}
	return syms
}

// extractGoVarConst extracts var_declaration and const_declaration nodes.
func extractGoVarConst(node *sitter.Node, src []byte, path string) []Symbol {
	var syms []Symbol
	for i := 0; i < int(node.ChildCount()); i++ {
		spec := node.Child(i)
		if spec.Type() != "var_spec" && spec.Type() != "const_spec" {
			continue
		}
		// The first identifier child is the variable/constant name
		for j := 0; j < int(spec.ChildCount()); j++ {
			child := spec.Child(j)
			if child.Type() == "identifier" {
				name := child.Content(src)
				startLine := int(spec.StartPoint().Row) + 1
				endLine := int(spec.EndPoint().Row) + 1
				id := GenerateSymbolID("", path, name, KindVariable, "")
				syms = append(syms, Symbol{
					ID:        id,
					Name:      name,
					Kind:      KindVariable,
					File:      path,
					StartLine: startLine,
					EndLine:   endLine,
					Signature: name,
					ProjectID: "",
				})
				break // only take the first identifier (the name)
			}
		}
	}
	return syms
}

// extractGoImports extracts import_declaration nodes and creates module symbols + import edges.
func extractGoImports(node *sitter.Node, src []byte, path string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	// Create a module symbol for the current file
	fileModuleID := GenerateSymbolID("", path, path, KindModule, "")

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		var importPath string
		switch child.Type() {
		case "import_spec":
			pathNode := child.ChildByFieldName("path")
			if pathNode != nil {
				importPath = strings.Trim(pathNode.Content(src), `"`)
			}
		case "interpreted_string_literal":
			importPath = strings.Trim(child.Content(src), `"`)
		}
		if importPath == "" {
			continue
		}

		modID := GenerateSymbolID("", importPath, importPath, KindModule, "")
		syms = append(syms, Symbol{
			ID:        modID,
			Name:      importPath,
			Kind:      KindModule,
			File:      path,
			StartLine: int(child.StartPoint().Row) + 1,
			EndLine:   int(child.EndPoint().Row) + 1,
			Signature: importPath,
			ProjectID: "",
		})
		edges = append(edges, Edge{
			FromID: fileModuleID,
			ToID:   modID,
			Kind:   EdgeImports,
			File:   path,
			Line:   int(child.StartPoint().Row) + 1,
		})
	}
	return syms, edges
}

// extractGoCallEdges walks the AST looking for call_expression nodes.
// It builds the enclosing-function map in a single top-down pass (O(n) in
// AST nodes) rather than walking up the parent chain for every call node.
func extractGoCallEdges(root *sitter.Node, src []byte, path string, symbolsByName map[string]string) []Edge {
	var edges []Edge
	fileModuleID := GenerateSymbolID("", path, path, KindModule, "")

	var walk func(node *sitter.Node, enclosingID string)
	walk = func(node *sitter.Node, enclosingID string) {
		// When we enter a function or method body, update the enclosing ID.
		switch node.Type() {
		case "function_declaration":
			if nameNode := node.ChildByFieldName("name"); nameNode != nil {
				if id, ok := symbolsByName[nameNode.Content(src)]; ok {
					enclosingID = id
				}
			}
		case "method_declaration":
			if nameNode := node.ChildByFieldName("name"); nameNode != nil {
				name := nameNode.Content(src)
				// Try qualified name first (ReceiverType.MethodName)
				if receiverNode := node.ChildByFieldName("receiver"); receiverNode != nil {
					receiverText := strings.Trim(receiverNode.Content(src), "()")
					parts := strings.Fields(receiverText)
					if len(parts) >= 2 {
						typeName := strings.TrimPrefix(parts[len(parts)-1], "*")
						qualifiedName := typeName + "." + name
						if id, ok := symbolsByName[qualifiedName]; ok {
							enclosingID = id
						}
					}
				}
				if enclosingID == "" {
					if id, ok := symbolsByName[name]; ok {
						enclosingID = id
					}
				}
			}
		case "call_expression":
			funcNode := node.ChildByFieldName("function")
			if funcNode != nil {
				calleeName := extractGoCalleeName(funcNode, src)
				if calleeName != "" {
					fromID := enclosingID
					if fromID == "" {
						fromID = fileModuleID
					}
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
			walk(node.Child(i), enclosingID)
		}
	}

	walk(root, "")
	return edges
}

// extractGoCalleeName extracts the callee name from a function node in a call expression.
func extractGoCalleeName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "identifier":
		return node.Content(src)
	case "selector_expression":
		// e.g. pkg.Func or receiver.Method
		fieldNode := node.ChildByFieldName("field")
		if fieldNode != nil {
			return fieldNode.Content(src)
		}
	}
	return ""
}

// buildGoFuncSignature builds a human-readable signature for a function.
func buildGoFuncSignature(node *sitter.Node, src []byte) string {
	nameNode := node.ChildByFieldName("name")
	paramsNode := node.ChildByFieldName("parameters")
	resultNode := node.ChildByFieldName("result")

	var sb strings.Builder
	sb.WriteString("func ")
	if nameNode != nil {
		sb.WriteString(nameNode.Content(src))
	}
	if paramsNode != nil {
		sb.WriteString(paramsNode.Content(src))
	}
	if resultNode != nil {
		sb.WriteString(" ")
		sb.WriteString(resultNode.Content(src))
	}
	return sb.String()
}

// buildGoMethodSignature builds a human-readable signature for a method.
func buildGoMethodSignature(node *sitter.Node, src []byte) string {
	receiverNode := node.ChildByFieldName("receiver")
	nameNode := node.ChildByFieldName("name")
	paramsNode := node.ChildByFieldName("parameters")
	resultNode := node.ChildByFieldName("result")

	var sb strings.Builder
	sb.WriteString("func ")
	if receiverNode != nil {
		sb.WriteString(receiverNode.Content(src))
		sb.WriteString(" ")
	}
	if nameNode != nil {
		sb.WriteString(nameNode.Content(src))
	}
	if paramsNode != nil {
		sb.WriteString(paramsNode.Content(src))
	}
	if resultNode != nil {
		sb.WriteString(" ")
		sb.WriteString(resultNode.Content(src))
	}
	return sb.String()
}
