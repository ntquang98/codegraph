//go:build cgo

package parse

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/javascript"
)

// JavaScriptExtractor extracts symbols and edges from JavaScript source files.
type JavaScriptExtractor struct{}

func (e *JavaScriptExtractor) Language() string     { return "javascript" }
func (e *JavaScriptExtractor) Extensions() []string { return []string{".js", ".jsx", ".mjs", ".cjs"} }

// Extract parses a JavaScript source file and returns all symbols and edges found.
func (e *JavaScriptExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
	lang := javascript.GetLanguage()
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
	symbolsByName := make(map[string]string)

	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		switch node.Type() {
		case "function_declaration":
			sym := extractJSFunction(node, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "lexical_declaration", "variable_declaration":
			syms := extractJSArrowFunctions(node, src, path)
			for _, s := range syms {
				sym := s
				symbols = append(symbols, sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "class_declaration":
			sym := extractJSClass(node, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
				methodSyms := extractJSClassMethods(node, src, path)
				for _, ms := range methodSyms {
					m := ms
					symbols = append(symbols, m)
					symbolsByName[m.Name] = m.ID
				}
			}
			return // children already processed
		case "import_statement":
			importSyms, importEdges := extractJSImports(node, src, path)
			symbols = append(symbols, importSyms...)
			edges = append(edges, importEdges...)
		case "call_expression":
			callEdge := extractJSCallEdge(node, src, path, symbolsByName)
			if callEdge != nil {
				edges = append(edges, *callEdge)
			}
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}
	walk(root)

	return symbols, edges, nil
}

func extractJSFunction(node *sitter.Node, src []byte, path string) *Symbol {
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
		Signature: "function " + name,
		ProjectID: "",
	}
}

func extractJSArrowFunctions(node *sitter.Node, src []byte, path string) []Symbol {
	var syms []Symbol
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() != "variable_declarator" {
			continue
		}
		nameNode := child.ChildByFieldName("name")
		valueNode := child.ChildByFieldName("value")
		if nameNode == nil || valueNode == nil {
			continue
		}
		if valueNode.Type() != "arrow_function" && valueNode.Type() != "function" {
			continue
		}
		name := nameNode.Content(src)
		startLine := int(child.StartPoint().Row) + 1
		endLine := int(child.EndPoint().Row) + 1
		id := GenerateSymbolID("", path, name, KindFunction, "")
		syms = append(syms, Symbol{
			ID:        id,
			Name:      name,
			Kind:      KindFunction,
			File:      path,
			StartLine: startLine,
			EndLine:   endLine,
			Signature: "const " + name + " = () => ...",
			ProjectID: "",
		})
	}
	return syms
}

func extractJSClass(node *sitter.Node, src []byte, path string) *Symbol {
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

func extractJSClassMethods(classNode *sitter.Node, src []byte, path string) []Symbol {
	var syms []Symbol

	bodyNode := classNode.ChildByFieldName("body")
	if bodyNode == nil {
		return syms
	}

	className := ""
	nameNode := classNode.ChildByFieldName("name")
	if nameNode != nil {
		className = nameNode.Content(src)
	}

	for i := 0; i < int(bodyNode.ChildCount()); i++ {
		member := bodyNode.Child(i)
		if member.Type() != "method_definition" {
			continue
		}
		methodNameNode := member.ChildByFieldName("name")
		if methodNameNode == nil {
			continue
		}
		methodName := methodNameNode.Content(src)
		qualifiedName := className + "." + methodName
		startLine := int(member.StartPoint().Row) + 1
		endLine := int(member.EndPoint().Row) + 1
		id := GenerateSymbolID("", path, qualifiedName, KindMethod, "")
		syms = append(syms, Symbol{
			ID:        id,
			Name:      qualifiedName,
			Kind:      KindMethod,
			File:      path,
			StartLine: startLine,
			EndLine:   endLine,
			Signature: qualifiedName + "()",
			ProjectID: "",
		})
	}
	return syms
}

func extractJSImports(node *sitter.Node, src []byte, path string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	fileModuleID := GenerateSymbolID("", path, path, KindModule, "")

	var importPath string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "string" {
			importPath = strings.Trim(child.Content(src), `"'`)
			break
		}
	}
	if importPath == "" {
		return syms, edges
	}

	modID := GenerateSymbolID("", importPath, importPath, KindModule, "")
	syms = append(syms, Symbol{
		ID:        modID,
		Name:      importPath,
		Kind:      KindModule,
		File:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
		Signature: importPath,
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

func extractJSCallEdge(node *sitter.Node, src []byte, path string, symbolsByName map[string]string) *Edge {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil {
		return nil
	}
	calleeName := extractJSCalleeName(funcNode, src)
	if calleeName == "" {
		return nil
	}

	fromID := GenerateSymbolID("", path, path, KindModule, "")
	toID, ok := symbolsByName[calleeName]
	if !ok {
		toID = GenerateSymbolID("", path, calleeName, KindFunction, "")
	}
	return &Edge{
		FromID: fromID,
		ToID:   toID,
		Kind:   EdgeCalls,
		File:   path,
		Line:   int(node.StartPoint().Row) + 1,
	}
}

func extractJSCalleeName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "identifier":
		return node.Content(src)
	case "member_expression":
		propNode := node.ChildByFieldName("property")
		if propNode != nil {
			return propNode.Content(src)
		}
	}
	return ""
}
