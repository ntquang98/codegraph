//go:build cgo

package parse

import (
	"context"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/csharp"
)

// CSharpExtractor extracts symbols and edges from C# source files.
type CSharpExtractor struct {
	parserPool sync.Pool
}

func (e *CSharpExtractor) Language() string     { return "csharp" }
func (e *CSharpExtractor) Extensions() []string { return []string{".cs"} }

func (e *CSharpExtractor) getParser() *sitter.Parser {
	if p, ok := e.parserPool.Get().(*sitter.Parser); ok {
		return p
	}
	p := sitter.NewParser()
	p.SetLanguage(csharp.GetLanguage())
	return p
}

func (e *CSharpExtractor) putParser(p *sitter.Parser) {
	e.parserPool.Put(p)
}

// Extract parses a C# source file and returns all symbols and edges found.
func (e *CSharpExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
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
	symbolsByName := make(map[string]string)

	var walk func(node *sitter.Node, enclosingClass string)
	walk = func(node *sitter.Node, enclosingClass string) {
		switch node.Type() {
		case "class_declaration":
			sym := extractCSClass(node, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
				// Extract base_list edges (implements/extends)
				classEdges := extractCSBaseList(node, src, path, sym.ID, symbolsByName)
				edges = append(edges, classEdges...)
				// Walk class body
				bodyNode := node.ChildByFieldName("body")
				if bodyNode != nil {
					for i := 0; i < int(bodyNode.ChildCount()); i++ {
						walk(bodyNode.Child(i), sym.Name)
					}
				}
			}
			return
		case "interface_declaration":
			sym := extractCSInterface(node, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
				// Extract base_list edges
				ifaceEdges := extractCSBaseList(node, src, path, sym.ID, symbolsByName)
				edges = append(edges, ifaceEdges...)
			}
			return
		case "method_declaration":
			sym := extractCSMethod(node, src, path, enclosingClass)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "constructor_declaration":
			sym := extractCSConstructor(node, src, path, enclosingClass)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "invocation_expression":
			callEdge := extractCSCallEdge(node, src, path, symbolsByName)
			if callEdge != nil {
				edges = append(edges, *callEdge)
			}
		case "using_directive":
			importSyms, importEdges := extractCSUsing(node, src, path)
			symbols = append(symbols, importSyms...)
			edges = append(edges, importEdges...)
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i), enclosingClass)
		}
	}
	walk(root, "")

	return symbols, edges, nil
}

func extractCSClass(node *sitter.Node, src []byte, path string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1
	id := GenerateSymbolID("", path, name, KindClass, "")
	return &Symbol{
		ID:        id,
		Name:      name,
		Kind:      KindClass,
		File:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: "class " + name,
		ProjectID: "",
	}
}

func extractCSInterface(node *sitter.Node, src []byte, path string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1
	id := GenerateSymbolID("", path, name, KindInterface, "")
	return &Symbol{
		ID:        id,
		Name:      name,
		Kind:      KindInterface,
		File:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: "interface " + name,
		ProjectID: "",
	}
}

func extractCSMethod(node *sitter.Node, src []byte, path string, enclosingClass string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	qualifiedName := name
	if enclosingClass != "" {
		qualifiedName = enclosingClass + "." + name
	}
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1
	id := GenerateSymbolID("", path, qualifiedName, KindMethod, "")
	return &Symbol{
		ID:        id,
		Name:      qualifiedName,
		Kind:      KindMethod,
		File:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: qualifiedName + "()",
		ProjectID: "",
	}
}

func extractCSConstructor(node *sitter.Node, src []byte, path string, enclosingClass string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	qualifiedName := name
	if enclosingClass != "" {
		qualifiedName = enclosingClass + "." + name
	}
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1
	id := GenerateSymbolID("", path, qualifiedName, KindMethod, "")
	return &Symbol{
		ID:        id,
		Name:      qualifiedName,
		Kind:      KindMethod,
		File:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: qualifiedName + "()",
		ProjectID: "",
	}
}

// extractCSBaseList extracts base_list nodes to create implements/extends edges.
// In C#, a class can extend one base class and implement multiple interfaces.
// We emit EdgeExtends for the first entry (assumed to be the base class if it doesn't start with 'I')
// and EdgeImplements for the rest. This is a heuristic; full resolution requires type info.
func extractCSBaseList(node *sitter.Node, src []byte, path string, fromID string, symbolsByName map[string]string) []Edge {
	var edges []Edge

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() != "base_list" {
			continue
		}
		entryIndex := 0
		for j := 0; j < int(child.ChildCount()); j++ {
			entry := child.Child(j)
			// base_list entries are type names separated by commas
			if entry.Type() == "," || entry.Type() == ":" {
				continue
			}
			targetName := extractCSTypeName(entry, src)
			if targetName == "" {
				continue
			}

			toID, ok := symbolsByName[targetName]
			if !ok {
				// Heuristic: interfaces typically start with 'I' followed by uppercase
				if strings.HasPrefix(targetName, "I") && len(targetName) > 1 {
					toID = GenerateSymbolID("", path, targetName, KindInterface, "")
				} else {
					toID = GenerateSymbolID("", path, targetName, KindClass, "")
				}
			}

			edgeKind := EdgeImplements
			if entryIndex == 0 {
				// First entry: check if it looks like a class (not an interface)
				if !strings.HasPrefix(targetName, "I") || len(targetName) == 1 {
					edgeKind = EdgeExtends
				}
			}

			edges = append(edges, Edge{
				FromID: fromID,
				ToID:   toID,
				Kind:   edgeKind,
				File:   path,
				Line:   int(entry.StartPoint().Row) + 1,
			})
			entryIndex++
		}
	}
	return edges
}

// extractCSTypeName extracts the type name from a base_list entry node.
func extractCSTypeName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "identifier":
		return node.Content(src)
	case "generic_name":
		// e.g. IList<T> — take just the identifier part
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child.Type() == "identifier" {
				return child.Content(src)
			}
		}
	case "qualified_name":
		// e.g. System.IDisposable — take the last part
		parts := strings.Split(node.Content(src), ".")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}
	return ""
}

// extractCSCallEdge extracts an invocation_expression node and creates a calls edge.
func extractCSCallEdge(node *sitter.Node, src []byte, path string, symbolsByName map[string]string) *Edge {
	// invocation_expression has a "function" field
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil {
		return nil
	}
	calleeName := extractCSCalleeName(funcNode, src)
	if calleeName == "" {
		return nil
	}

	fromID := GenerateSymbolID("", path, path, KindModule, "")
	toID, ok := symbolsByName[calleeName]
	if !ok {
		toID = GenerateSymbolID("", path, calleeName, KindMethod, "")
	}
	return &Edge{
		FromID: fromID,
		ToID:   toID,
		Kind:   EdgeCalls,
		File:   path,
		Line:   int(node.StartPoint().Row) + 1,
	}
}

func extractCSCalleeName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "identifier":
		return node.Content(src)
	case "member_access_expression":
		nameNode := node.ChildByFieldName("name")
		if nameNode != nil {
			return nameNode.Content(src)
		}
	}
	return ""
}

// extractCSUsing extracts using_directive nodes and creates module symbols + import edges.
func extractCSUsing(node *sitter.Node, src []byte, path string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	fileModuleID := GenerateSymbolID("", path, path, KindModule, "")

	// Find the namespace name
	var namespaceName string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "identifier", "qualified_name":
			namespaceName = child.Content(src)
		case "name_equals":
			// using Alias = Namespace; — skip alias directives
			return syms, edges
		}
	}
	if namespaceName == "" {
		return syms, edges
	}

	modID := GenerateSymbolID("", namespaceName, namespaceName, KindModule, "")
	syms = append(syms, Symbol{
		ID:        modID,
		Name:      namespaceName,
		Kind:      KindModule,
		File:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
		Signature: namespaceName,
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
