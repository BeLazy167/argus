package graph

import (
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

// parseGoAST extracts symbols and edges from Go source using the stdlib AST parser.
// Returns nil, nil if parsing fails so the caller can fall back to regex.
func parseGoAST(filePath, content string) (syms []Symbol, edges []Edge) {
	operationID := obs.NewLogID()
	started := time.Now()
	slog.Debug("graph Go AST parse started", "operation_id", operationID, "file", filePath, "source_bytes", len(content))
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, content, parser.ParseComments)
	if err != nil {
		slog.Warn("graph Go AST parse failed; regex fallback selected", "operation_id", operationID,
			"file", filePath, "source_bytes", len(content), "parser", "go_ast", "fallback", "go_regex",
			"duration_ms", time.Since(started).Milliseconds(), "error", err)
		return nil, nil
	}

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

			// Walk body for call edges. Receiver calls use the declared receiver
			// type, so a.Handle resolves to Alpha.Handle rather than a name shared
			// by every receiver in the file.
			if decl.Body != nil {
				receiverVar := ""
				if decl.Recv != nil && decl.Recv.NumFields() > 0 && len(decl.Recv.List[0].Names) > 0 {
					receiverVar = decl.Recv.List[0].Names[0].Name
				}
				edges = append(edges, extractCallEdgesAST(sym.Name, receiverVar, receiverIdentity(sym.Receiver), decl.Body)...)
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

	slog.Debug("graph Go AST parse completed", "operation_id", operationID, "file", filePath,
		"parser", "go_ast", "import_count", len(f.Imports), "symbol_count", len(syms), "edge_count", len(edges),
		"duration_ms", time.Since(started).Milliseconds())
	return syms, edges
}

func funcDeclSymbol(fset *token.FileSet, filePath string, decl *ast.FuncDecl) Symbol {
	name := decl.Name.Name
	kind := "function"
	receiver := ""

	if decl.Recv != nil && decl.Recv.NumFields() > 0 {
		kind = "method"
		receiver = receiverTypeName(decl.Recv.List[0].Type)
		name = qualifySymbolName(receiverIdentity(receiver), name)
	}

	params := formatFieldList(decl.Type.Params)
	returnType := formatResults(decl.Type.Results)
	// Qualification adds the receiver before the declaration name. Go export
	// visibility is defined only by the declaration identifier itself.
	vis := visibility(decl.Name.Name)
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

func extractCallEdgesAST(sourceName, receiverVar, receiverType string, body *ast.BlockStmt) []Edge {
	var edges []Edge
	seen := make(map[string]bool)

	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		target := callTargetName(call, receiverVar, receiverType)
		if target == "" || target == sourceName || isBuiltin(target) || seen[target] {
			return true
		}
		seen[target] = true
		edges = append(edges, Edge{SourceName: sourceName, TargetName: target, Kind: "calls"})
		return true
	})
	return edges
}

func callTargetName(call *ast.CallExpr, receiverVar, receiverType string) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		qualifier := selectorQualifier(fn.X)
		if qualifier == receiverVar && receiverType != "" {
			qualifier = receiverType
		}
		if qualifier == "" {
			return fn.Sel.Name
		}
		return qualifier + "." + fn.Sel.Name
	default:
		return ""
	}
}

// selectorQualifier extracts the stable type/package portion of a selector.
// Composite literals are important here: Alpha{}.Handle() must identify the
// Alpha method even when Beta.Handle exists in the same file.
func selectorQualifier(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		prefix := selectorQualifier(x.X)
		if prefix == "" {
			return x.Sel.Name
		}
		return prefix + "." + x.Sel.Name
	case *ast.CompositeLit:
		return receiverIdentity(exprName(x.Type))
	case *ast.ParenExpr:
		return selectorQualifier(x.X)
	case *ast.UnaryExpr:
		return selectorQualifier(x.X)
	case *ast.IndexExpr:
		return selectorQualifier(x.X)
	case *ast.IndexListExpr:
		return selectorQualifier(x.X)
	case *ast.CallExpr:
		return callTargetName(x, "", "")
	default:
		return ""
	}
}
