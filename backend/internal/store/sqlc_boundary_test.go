package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestStoreSQLCBoundaryHasNoPositionalMappingsOrExportedQueries(t *testing.T) {
	files, err := parser.ParseDir(token.NewFileSet(), ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse store package: %v", err)
	}
	pkg := files["store"]
	if pkg == nil {
		t.Fatal("store package not found")
	}
	for filename, file := range pkg.Files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				if value.Sel.Name == "RowToStructByPos" {
					t.Errorf("%s retains pgx.RowToStructByPos", filename)
				}
			case *ast.TypeSpec:
				if value.Name.Name != "Store" {
					return true
				}
				structure, ok := value.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, field := range structure.Fields.List {
					for _, name := range field.Names {
						if name.Name == "Q" {
							t.Errorf("Store exposes generated queries through field %s", name.Name)
						}
					}
				}
			}
			return true
		})
	}
}
