//go:build cgo

package parse

import (
	"context"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
)

// PythonExtractor extracts symbols and edges from Python source files.
type PythonExtractor struct {
	parserPool sync.Pool
}

func (e *PythonExtractor) Language() string     { return "python" }
func (e *PythonExtractor) Extensions() []string { return []string{".py"} }

func (e *PythonExtractor) getParser() *sitter.Parser {
	if p, ok := e.parserPool.Get().(*sitter.Parser); ok {
		return p
	}
	p := sitter.NewParser()
	p.SetLanguage(python.GetLanguage())
	return p
}

func (e *PythonExtractor) putParser(p *sitter.Parser) {
	e.parserPool.Put(p)
}

// Extract parses a Python source file and returns all symbols and edges found.
func (e *PythonExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
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

	var walk func(node *sitter.Node, insideClass string)
	walk = func(node *sitter.Node, insideClass string) {
		switch node.Type() {
		case "function_definition":
			sym := extractPyFunction(node, src, path, insideClass)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
			}
		case "decorated_definition":
			// Could be a decorated function or class
			inner := extractPyDecoratedDef(node, src, path, insideClass)
			if inner != nil {
				symbols = append(symbols, *inner)
				symbolsByName[inner.Name] = inner.ID
			}
		case "class_definition":
			sym := extractPyClass(node, src, path)
			if sym != nil {
				symbols = append(symbols, *sym)
				symbolsByName[sym.Name] = sym.ID
				// Walk class body with class context
				bodyNode := node.ChildByFieldName("body")
				if bodyNode != nil {
					for i := 0; i < int(bodyNode.ChildCount()); i++ {
						walk(bodyNode.Child(i), sym.Name)
					}
				}
			}
			return // children already processed above
		case "call":
			callEdge := extractPyCallEdge(node, src, path, symbolsByName)
			if callEdge != nil {
				edges = append(edges, *callEdge)
			}
		case "import_statement":
			importSyms, importEdges := extractPyImport(node, src, path)
			symbols = append(symbols, importSyms...)
			edges = append(edges, importEdges...)
		case "import_from_statement":
			importSyms, importEdges := extractPyImportFrom(node, src, path)
			symbols = append(symbols, importSyms...)
			edges = append(edges, importEdges...)
		}
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i), insideClass)
		}
	}
	walk(root, "")

	return symbols, edges, nil
}

// extractPyFunction extracts a function_definition node.
// If insideClass is non-empty, the function is treated as a method.
func extractPyFunction(node *sitter.Node, src []byte, path string, insideClass string) *Symbol {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	kind := KindFunction
	qualifiedName := name
	if insideClass != "" {
		kind = KindMethod
		qualifiedName = insideClass + "." + name
	}

	id := GenerateSymbolID("", path, qualifiedName, kind, "")
	return &Symbol{
		ID:        id,
		Name:      qualifiedName,
		Kind:      kind,
		File:      path,
		StartLine: startLine,
		EndLine:   endLine,
		Signature: "def " + qualifiedName + "(...)",
		ProjectID: "",
	}
}

// extractPyDecoratedDef handles decorated_definition nodes (decorated functions/classes).
func extractPyDecoratedDef(node *sitter.Node, src []byte, path string, insideClass string) *Symbol {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "function_definition":
			return extractPyFunction(child, src, path, insideClass)
		case "class_definition":
			return extractPyClass(child, src, path)
		}
	}
	return nil
}

// extractPyClass extracts a class_definition node.
func extractPyClass(node *sitter.Node, src []byte, path string) *Symbol {
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

// extractPyCallEdge extracts a call node and creates a calls edge.
func extractPyCallEdge(node *sitter.Node, src []byte, path string, symbolsByName map[string]string) *Edge {
	funcNode := node.ChildByFieldName("function")
	if funcNode == nil {
		return nil
	}
	calleeName := extractPyCalleeName(funcNode, src)
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

func extractPyCalleeName(node *sitter.Node, src []byte) string {
	switch node.Type() {
	case "identifier":
		return node.Content(src)
	case "attribute":
		// e.g. obj.method
		attrNode := node.ChildByFieldName("attribute")
		if attrNode != nil {
			return attrNode.Content(src)
		}
	}
	return ""
}

// extractPyImport handles "import foo" and "import foo as bar" statements.
func extractPyImport(node *sitter.Node, src []byte, path string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	fileModuleID := GenerateSymbolID("", path, path, KindModule, "")

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		var importPath string
		switch child.Type() {
		case "dotted_name":
			importPath = child.Content(src)
		case "aliased_import":
			nameNode := child.ChildByFieldName("name")
			if nameNode != nil {
				importPath = nameNode.Content(src)
			}
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

// extractPyImportFrom handles "from foo import bar" statements.
func extractPyImportFrom(node *sitter.Node, src []byte, path string) ([]Symbol, []Edge) {
	var syms []Symbol
	var edges []Edge

	fileModuleID := GenerateSymbolID("", path, path, KindModule, "")

	// Find the module name (dotted_name after "from")
	var moduleName string
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "dotted_name" || child.Type() == "relative_import" {
			moduleName = strings.TrimPrefix(child.Content(src), ".")
			break
		}
	}
	if moduleName == "" {
		return syms, edges
	}

	modID := GenerateSymbolID("", moduleName, moduleName, KindModule, "")
	syms = append(syms, Symbol{
		ID:        modID,
		Name:      moduleName,
		Kind:      KindModule,
		File:      path,
		StartLine: int(node.StartPoint().Row) + 1,
		EndLine:   int(node.EndPoint().Row) + 1,
		Signature: moduleName,
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
