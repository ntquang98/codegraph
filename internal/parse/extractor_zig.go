//go:build cgo

package parse

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	zigbinding "github.com/codegraph-cli/codegraph/internal/parse/zig"
)

// getZigLanguage returns the tree-sitter Language for Zig using the embedded parser.
func getZigLanguage() *sitter.Language {
	return zigbinding.GetLanguage()
}

// ZigExtractor extracts symbols and edges from Zig source files using tree-sitter.
type ZigExtractor struct{}

func (e *ZigExtractor) Language() string     { return "zig" }
func (e *ZigExtractor) Extensions() []string { return []string{".zig"} }

// Extract parses a Zig source file and returns all symbols and edges found.
func (e *ZigExtractor) Extract(path string, src []byte) ([]Symbol, []Edge, error) {
	lang := getZigLanguage()
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

	// Walk top-level declarations in source_file
	// The grammar: source_file → _ContainerMembers → _ContainerDeclarations → Decl | TestDecl | ComptimeDecl
	// Each Decl contains either FnProto+Block (function) or VarDecl (variable/const)
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		extractZigTopLevel(child, src, path, fileModuleID, &symbols, &edges, symbolsByName)
	}

	// Second pass: extract call edges from the entire AST
	callEdges := extractZigCallEdges(root, src, path, symbolsByName, fileModuleID)
	edges = append(edges, callEdges...)

	return symbols, edges, nil
}

// extractZigTopLevel processes a top-level node from source_file.
// The grammar wraps declarations in Decl nodes, which contain FnProto or VarDecl.
func extractZigTopLevel(node *sitter.Node, src []byte, path string, fileModuleID string,
	symbols *[]Symbol, edges *[]Edge, symbolsByName map[string]string) {

	switch node.Type() {
	case "Decl":
		// A Decl can contain FnProto (function) or VarDecl (const/var)
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			switch child.Type() {
			case "FnProto":
				sym := extractZigFunction(child, src, path)
				if sym != nil {
					*symbols = append(*symbols, *sym)
					symbolsByName[sym.Name] = sym.ID
				}
			case "VarDecl":
				syms, edgs := extractZigVarDecl(child, src, path, fileModuleID, symbolsByName)
				*symbols = append(*symbols, syms...)
				*edges = append(*edges, edgs...)
			}
		}
	}
}

// extractZigFunction extracts a FnProto node as a KindFunction symbol.
// The "function" field holds the IDENTIFIER (function name).
func extractZigFunction(node *sitter.Node, src []byte, path string) *Symbol {
	nameNode := node.ChildByFieldName("function")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(src)
	if name == "" {
		return nil
	}
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

// extractZigVarDecl processes a VarDecl node.
// The "variable_type_function" field holds the IDENTIFIER (variable/const name).
//
// Grammar: VarDecl = (const|var) IDENTIFIER [: TypeExpr] [= Expr] ;
// The value Expr is wrapped as: _Expr → _CurlySuffixExpr → _TypeExpr → ErrorUnionExpr → SuffixExpr
// ContainerDecl appears as a direct child of SuffixExpr (via _PrimaryTypeExpr).
// @import appears as BUILTINIDENTIFIER + FnCallArguments inside SuffixExpr.
//
// We recursively search the VarDecl subtree for ContainerDecl and @import patterns.
func extractZigVarDecl(node *sitter.Node, src []byte, path string, fileModuleID string,
	symbolsByName map[string]string) ([]Symbol, []Edge) {

	var syms []Symbol
	var edges []Edge

	nameNode := node.ChildByFieldName("variable_type_function")
	if nameNode == nil {
		return syms, edges
	}
	name := nameNode.Content(src)
	if name == "" {
		return syms, edges
	}

	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	// Search for ContainerDecl in the subtree (struct/enum/union type)
	containerNode := findNodeByTypeRecursive(node, "ContainerDecl")
	if containerNode != nil {
		containerKind := getZigContainerKind(containerNode, src)
		if containerKind == "struct" || containerKind == "enum" || containerKind == "union" {
			id := GenerateSymbolID("", path, name, KindType, "")
			sym := Symbol{
				ID:        id,
				Name:      name,
				Kind:      KindType,
				File:      path,
				StartLine: startLine,
				EndLine:   endLine,
				Signature: name,
				ProjectID: "",
			}
			syms = append(syms, sym)
			symbolsByName[name] = id

			// Extract struct methods if it's a struct or union
			if containerKind == "struct" || containerKind == "union" {
				methodSyms := extractZigStructMethods(containerNode, src, path, name)
				for _, ms := range methodSyms {
					ms := ms
					syms = append(syms, ms)
					symbolsByName[ms.Name] = ms.ID
				}
			}
			return syms, edges
		}
	}

	// Search for @import builtin call in the subtree
	importPath := extractZigImportPath(node, src)
	if importPath != "" {
		modID := GenerateSymbolID("", importPath, importPath, KindModule, "")
		syms = append(syms, Symbol{
			ID:        modID,
			Name:      importPath,
			Kind:      KindModule,
			File:      path,
			StartLine: startLine,
			EndLine:   endLine,
			Signature: importPath,
			ProjectID: "",
		})
		edges = append(edges, Edge{
			FromID: fileModuleID,
			ToID:   modID,
			Kind:   EdgeImports,
			File:   path,
			Line:   startLine,
		})
	}

	return syms, edges
}

// findNodeByTypeRecursive performs a depth-first search for the first node
// with the given type in the subtree rooted at node.
func findNodeByTypeRecursive(node *sitter.Node, nodeType string) *sitter.Node {
	if node == nil {
		return nil
	}
	if node.Type() == nodeType {
		return node
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		result := findNodeByTypeRecursive(node.Child(i), nodeType)
		if result != nil {
			return result
		}
	}
	return nil
}

// getZigContainerKind returns "struct", "enum", "union", or "" for a ContainerDecl node.
func getZigContainerKind(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "ContainerDeclType" {
			text := child.Content(src)
			if strings.HasPrefix(text, "struct") {
				return "struct"
			}
			if strings.HasPrefix(text, "enum") {
				return "enum"
			}
			if strings.HasPrefix(text, "union") {
				return "union"
			}
		}
	}
	return ""
}

// extractZigImportPath searches a node subtree for a BUILTINIDENTIFIER "@import"
// followed by FnCallArguments containing a string literal, and returns the import path.
func extractZigImportPath(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}

	// Check if this node is a SuffixExpr that starts with @import
	if node.Type() == "SuffixExpr" {
		builtinNode := findChildByType(node, "BUILTINIDENTIFIER")
		if builtinNode != nil && builtinNode.Content(src) == "@import" {
			argsNode := findChildByType(node, "FnCallArguments")
			if argsNode != nil {
				return extractZigStringArg(argsNode, src)
			}
		}
	}

	// Recurse into children
	for i := 0; i < int(node.ChildCount()); i++ {
		result := extractZigImportPath(node.Child(i), src)
		if result != "" {
			return result
		}
	}
	return ""
}

// extractZigStringArg extracts the first string literal argument from FnCallArguments.
func extractZigStringArg(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "STRINGLITERALSINGLE" {
			text := child.Content(src)
			// Strip surrounding quotes
			text = strings.Trim(text, `"`)
			return text
		}
	}
	return ""
}

// extractZigStructMethods walks a ContainerDecl (struct) looking for Decl children
// that contain FnProto nodes, and returns them as KindMethod symbols qualified as
// "StructName.fn_name".
func extractZigStructMethods(containerNode *sitter.Node, src []byte, path string, structName string) []Symbol {
	var syms []Symbol

	for i := 0; i < int(containerNode.ChildCount()); i++ {
		child := containerNode.Child(i)
		if child.Type() != "Decl" {
			continue
		}
		// Look for FnProto inside the Decl
		for j := 0; j < int(child.ChildCount()); j++ {
			grandchild := child.Child(j)
			if grandchild.Type() == "FnProto" {
				nameNode := grandchild.ChildByFieldName("function")
				if nameNode == nil {
					continue
				}
				fnName := nameNode.Content(src)
				if fnName == "" {
					continue
				}
				qualifiedName := structName + "." + fnName
				startLine := int(grandchild.StartPoint().Row) + 1
				endLine := int(grandchild.EndPoint().Row) + 1

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
		}
	}

	return syms
}

// extractZigCallEdges walks the entire AST looking for function call nodes and
// produces EdgeCalls edges.
//
// In the Zig grammar, a function call is represented as a SuffixExpr that contains
// FnCallArguments. The callee is either:
//   - A SuffixExpr with a "variable_type_function" field (simple call: foo(...))
//   - A SuffixExpr followed by FieldOrFnCall with "function_call" field (method call: obj.method(...))
func extractZigCallEdges(root *sitter.Node, src []byte, path string, symbolsByName map[string]string, fileModuleID string) []Edge {
	var edges []Edge

	var walk func(node *sitter.Node)
	walk = func(node *sitter.Node) {
		if node.Type() == "SuffixExpr" {
			calleeName := extractZigCalleeName(node, src)
			if calleeName != "" {
				fromID := resolveZigEnclosingID(node, src, path, symbolsByName, fileModuleID)
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
		for i := 0; i < int(node.ChildCount()); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)
	return edges
}

// extractZigCalleeName extracts the callee name from a SuffixExpr node that
// represents a function call (i.e., has FnCallArguments or FieldOrFnCall with
// function_call field).
func extractZigCalleeName(node *sitter.Node, src []byte) string {
	if node.Type() != "SuffixExpr" {
		return ""
	}

	hasFnCall := false
	calleeName := ""

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		switch child.Type() {
		case "FnCallArguments":
			hasFnCall = true
		case "FieldOrFnCall":
			// method call: obj.method(...)
			fnCallNode := child.ChildByFieldName("function_call")
			if fnCallNode != nil {
				calleeName = fnCallNode.Content(src)
				hasFnCall = true
			}
		case "IDENTIFIER":
			// simple call: foo(...)
			calleeName = child.Content(src)
		}
	}

	// Also check the variable_type_function field for simple calls
	if calleeName == "" {
		vtfNode := node.ChildByFieldName("variable_type_function")
		if vtfNode != nil {
			calleeName = vtfNode.Content(src)
		}
	}

	// Skip @import and other builtins
	if strings.HasPrefix(calleeName, "@") {
		return ""
	}

	if hasFnCall && calleeName != "" {
		return calleeName
	}
	return ""
}

// resolveZigEnclosingID walks up the AST from node to find the nearest enclosing
// function (FnProto inside a Decl), returning its symbol ID.
// Falls back to fileModuleID if no enclosing function is found.
func resolveZigEnclosingID(node *sitter.Node, src []byte, path string, symbolsByName map[string]string, fileModuleID string) string {
	cur := node.Parent()
	for cur != nil {
		if cur.Type() == "Decl" {
			// Look for FnProto inside this Decl
			for i := 0; i < int(cur.ChildCount()); i++ {
				child := cur.Child(i)
				if child.Type() == "FnProto" {
					nameNode := child.ChildByFieldName("function")
					if nameNode != nil {
						fnName := nameNode.Content(src)
						// Check if this is a struct method (parent of Decl is ContainerDecl)
						parent := cur.Parent()
						if parent != nil && parent.Type() == "ContainerDecl" {
							// Find the struct name by going up to VarDecl
							grandParent := parent.Parent()
							if grandParent != nil && grandParent.Type() == "VarDecl" {
								structNameNode := grandParent.ChildByFieldName("variable_type_function")
								if structNameNode != nil {
									qualifiedName := structNameNode.Content(src) + "." + fnName
									if id, ok := symbolsByName[qualifiedName]; ok {
										return id
									}
								}
							}
						}
						// Top-level function
						if id, ok := symbolsByName[fnName]; ok {
							return id
						}
					}
				}
			}
		}
		cur = cur.Parent()
	}
	return fileModuleID
}

// findChildByType finds the first direct child of node with the given type.
func findChildByType(node *sitter.Node, nodeType string) *sitter.Node {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == nodeType {
			return child
		}
	}
	return nil
}
