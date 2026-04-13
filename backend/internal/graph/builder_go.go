package graph

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"unicode"
)

// parseGoAST extracts symbols and edges from Go source using the stdlib AST parser.
// Returns nil, nil if parsing fails so the caller can fall back to regex.
func parseGoAST(filePath, content string) ([]Symbol, []Edge) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		return nil, nil
	}

	var syms []Symbol
	var edges []Edge

	// Import edges
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		edges = append(edges, Edge{SourceName: filePath, TargetName: path, Kind: "imports"})
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			sym := funcDeclSymbol(fset, filePath, decl)
			syms = append(syms, sym)

			// Walk body for call edges
			if decl.Body != nil {
				edges = append(edges, extractCallEdgesAST(sym.Name, decl.Body)...)
			}
			return false

		case *ast.GenDecl:
			if decl.Tok != token.TYPE {
				return true
			}
			for _, spec := range decl.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				sym := typeSpecSymbol(fset, filePath, ts, decl)
				syms = append(syms, sym)
			}
			return false
		}
		return true
	})

	return syms, edges
}

func funcDeclSymbol(fset *token.FileSet, filePath string, decl *ast.FuncDecl) Symbol {
	name := decl.Name.Name
	kind := "function"
	receiver := ""

	if decl.Recv != nil && decl.Recv.NumFields() > 0 {
		kind = "method"
		receiver = receiverTypeName(decl.Recv.List[0].Type)
	}

	params := formatFieldList(decl.Type.Params)
	returnType := formatResults(decl.Type.Results)
	vis := visibility(name)
	scope := "package"
	if decl.Recv != nil {
		scope = "method"
	}

	start := fset.Position(decl.Pos()).Line
	end := fset.Position(decl.End()).Line

	return Symbol{
		Kind:       kind,
		Name:       name,
		FilePath:   filePath,
		LineStart:  start,
		LineEnd:    end,
		ReturnType: returnType,
		Params:     params,
		Visibility: vis,
		Receiver:   receiver,
		Scope:      scope,
	}
}

func typeSpecSymbol(fset *token.FileSet, filePath string, ts *ast.TypeSpec, decl *ast.GenDecl) Symbol {
	name := ts.Name.Name
	kind := "type"

	switch ts.Type.(type) {
	case *ast.InterfaceType:
		kind = "interface"
	case *ast.StructType:
		kind = "type"
	}

	start := fset.Position(ts.Pos()).Line
	end := fset.Position(ts.Type.End()).Line

	return Symbol{
		Kind:       kind,
		Name:       name,
		FilePath:   filePath,
		LineStart:  start,
		LineEnd:    end,
		Visibility: visibility(name),
		Scope:      "package",
	}
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + exprName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return exprName(t.X)
	case *ast.IndexListExpr:
		return exprName(t.X)
	default:
		return ""
	}
}

func exprName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return exprName(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + exprName(t.X)
	case *ast.ArrayType:
		return "[]" + exprName(t.Elt)
	case *ast.MapType:
		return "map[" + exprName(t.Key) + "]" + exprName(t.Value)
	case *ast.Ellipsis:
		return "..." + exprName(t.Elt)
	case *ast.FuncType:
		return "func" + formatFieldList(t.Params) + formatResults(t.Results)
	case *ast.InterfaceType:
		return "interface{}"
	default:
		return ""
	}
}

func formatFieldList(fl *ast.FieldList) string {
	if fl == nil || fl.NumFields() == 0 {
		return "()"
	}
	var parts []string
	for _, field := range fl.List {
		typeName := exprName(field.Type)
		if len(field.Names) == 0 {
			parts = append(parts, typeName)
		} else {
			for _, n := range field.Names {
				parts = append(parts, n.Name+" "+typeName)
			}
		}
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func formatResults(fl *ast.FieldList) string {
	if fl == nil || fl.NumFields() == 0 {
		return ""
	}
	var parts []string
	for _, field := range fl.List {
		typeName := exprName(field.Type)
		if len(field.Names) == 0 {
			parts = append(parts, typeName)
		} else {
			for _, n := range field.Names {
				parts = append(parts, n.Name+" "+typeName)
			}
		}
	}
	if fl.NumFields() == 1 && len(fl.List[0].Names) == 0 {
		return parts[0]
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func visibility(name string) string {
	if len(name) == 0 {
		return "unexported"
	}
	if unicode.IsUpper([]rune(name)[0]) {
		return "exported"
	}
	return "unexported"
}

func extractCallEdgesAST(sourceName string, body *ast.BlockStmt) []Edge {
	var edges []Edge
	seen := make(map[string]bool)

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		target := callTargetName(call)
		if target == "" || target == sourceName || isBuiltin(target) || seen[target] {
			return true
		}
		seen[target] = true
		edges = append(edges, Edge{SourceName: sourceName, TargetName: target, Kind: "calls"})
		return true
	})
	return edges
}

func callTargetName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if ident, ok := fn.X.(*ast.Ident); ok {
			return ident.Name + "." + fn.Sel.Name
		}
		return fn.Sel.Name
	default:
		return ""
	}
}
