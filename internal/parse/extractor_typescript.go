//go:build cgo

package parse

import (
	"context"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// TypeScriptExtractor extracts symbols and edges from TypeScript source files.
type TypeScriptExtractor struct {
	parserPool sync.Pool
}

func (e *TypeScriptExtractor) Language() string     { return "typescript" }
func (e *TypeScriptExtractor) Extensions() []string { return []string{".ts", ".tsx"} }

func (e *TypeScriptExtractor) getParser() *sitter.Parser {
	if p, ok := e.parserPool.Get().(*sitter.Parser); ok {
		return p
	}
	p := sitter.NewParser()
	p.SetLanguage(typescript.GetLanguage())
	return p
}

func (e *TypeScriptExtractor) putParser(p *sitter.Parser) {
	e.parserPool.Put(p)
}

// Extract parses a TypeScript source file and returns all symbols and edges found.
func (e *TypeScriptExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
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

	// Walk the AST to collect declarations
	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		switch node.Type() {
		case "function_declaration":
			sym := extractTSFunction(node, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "lexical_declaration", "variable_declaration":
			// Arrow functions: const foo = () => {}
			syms := extractTSArrowFunctions(node, src, path)
			for _, s := range syms {
				sym := s
				symbols = append(symbols, sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "class_declaration":
			sym := extractTSClass(node, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
				// Extract methods inside the class
				methodSyms, methodEdges := extractTSClassMembers(node, src, path, sym.ID)
				symbols = append(symbols, methodSyms...)
				edges = append(edges, methodEdges...)
				for _, ms := range methodSyms {
					symbolsByName[ms.Name] = ms.ID
				}
				// Extract extends/implements edges
				classEdges := extractTSClassEdges(node, src, path, sym.ID, symbolsByName)
				edges = append(edges, classEdges...)
			}
			return // children already processed
		case "interface_declaration":
			sym := extractTSInterface(node, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "import_statement":
			importSyms, importEdges := extractTSImports(node, src, path)
			symbols = append(symbols, importSyms...)
			edges = append(edges, importEdges...)
		case "call_expression":
			callEdge := extractTSCallEdge(node, src, path, symbolsByName)
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

func extractTSFunction(node *sitter.Node, src []byte, path string) *Symbol {
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

func extractTSArrowFunctions(node *sitter.Node, src []byte, path string) []Symbol {
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
		if valueNode.Type() != "arrow_function" {
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

func extractTSClass(node *sitter.Node, src []byte, path string) *Symbol {
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

func extractTSClassMembers(classNode *sitter.Node, src []byte, path string, classID string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	bodyNode := classNode.ChildByFieldName("body")
	if bodyNode == nil {
		return syms, edges
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
	return syms, edges
}

func extractTSClassEdges(classNode *sitter.Node, src []byte, path string, classID string, symbolsByName map[string]string) []Edge {
	var edges []Edge

	for i := 0; i < int(classNode.ChildCount()); i++ {
		child := classNode.Child(i)
		switch child.Type() {
		case "class_heritage":
			// extends and implements clauses
			for j := 0; j < int(child.ChildCount()); j++ {
				clause := child.Child(j)
				switch clause.Type() {
				case "extends_clause":
					for k := 0; k < int(clause.ChildCount()); k++ {
						typeNode := clause.Child(k)
						if typeNode.Type() == "identifier" || typeNode.Type() == "type_identifier" {
							targetName := typeNode.Content(src)
							toID, ok := symbolsByName[targetName]
							if !ok {
								toID = GenerateSymbolID("", path, targetName, KindClass, "")
							}
							edges = append(edges, Edge{
								FromID: classID,
								ToID:   toID,
								Kind:   EdgeExtends,
								File:   path,
								Line:   int(clause.StartPoint().Row) + 1,
							})
						}
					}
				case "implements_clause":
					for k := 0; k < int(clause.ChildCount()); k++ {
						typeNode := clause.Child(k)
						if typeNode.Type() == "type_identifier" || typeNode.Type() == "identifier" {
							targetName := typeNode.Content(src)
							toID, ok := symbolsByName[targetName]
							if !ok {
								toID = GenerateSymbolID("", path, targetName, KindInterface, "")
							}
							edges = append(edges, Edge{
								FromID: classID,
								ToID:   toID,
								Kind:   EdgeImplements,
								File:   path,
								Line:   int(clause.StartPoint().Row) + 1,
							})
						}
					}
				}
			}
		}
	}
	return edges
}

func extractTSInterface(node *sitter.Node, src []byte, path string) *Symbol {
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

func extractTSImports(node *sitter.Node, src []byte, path string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	fileModuleID := GenerateSymbolID("", path, path, KindModule, "")

	// Find the source string (the module path)
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

func extractTSCallEdge(node *sitter.Node, src []byte, path string, symbolsByName map[string]string) *Edge {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil {
		return nil
	}
	calleeName := extractTSCalleeName(funcNode, src)
	if calleeName == "" {
		return nil
	}

	// Use file module as the "from" (simplified — no enclosing function tracking here)
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

func extractTSCalleeName(node *sitter.Node, src []byte) string {
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
