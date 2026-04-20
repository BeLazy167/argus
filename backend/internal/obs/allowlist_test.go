package obs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// slogAttrHelpers are the slog package functions whose first argument names a
// structured log attribute key. Each one takes (key string, value X) and the
// first argument is what we must guard.
var slogAttrHelpers = map[string]struct{}{
	"String":   {},
	"Int":      {},
	"Int64":    {},
	"Bool":     {},
	"Any":      {},
	"Duration": {},
	"Time":     {},
	"Float64":  {},
	"Uint64":   {},
}

// TestSlogAttrsOnAllowlist scans backend/internal for slog.String/Int/... calls
// and asserts every literal first argument is covered by AllowedKeys or
// DenyKeys. Dynamic keys (non-literal) are logged and skipped — they cannot
// be reasoned about statically.
//
// Run from within the obs package with its working directory as usual
// (go test sets CWD to the package dir). Walk target is ../../internal
// (parent internal/ root).
func TestSlogAttrsOnAllowlist(t *testing.T) {
	// Working dir inside a package test is the package dir. Walk the parent.
	root := filepath.Join("..")
	fset := token.NewFileSet()

	type violation struct {
		file string
		line int
		key  string
	}
	var violations []violation
	var dynamic int

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip vendor and this package (obs) — obs defines the allowlist
			// itself and may reference keys for unit tests / doc.
			name := d.Name()
			if name == "vendor" || name == "obs" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok || x.Name != "slog" {
				return true
			}
			if _, isHelper := slogAttrHelpers[sel.Sel.Name]; !isHelper {
				return true
			}
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				dynamic++
				return true
			}
			key, unqErr := strconv.Unquote(lit.Value)
			if unqErr != nil {
				return true
			}
			if _, allowed := AllowedKeys[key]; allowed {
				return true
			}
			if _, denied := DenyKeys[key]; denied {
				return true
			}
			pos := fset.Position(lit.Pos())
			violations = append(violations, violation{file: pos.Filename, line: pos.Line, key: key})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk failed: %v", err)
	}
	if dynamic > 0 {
		t.Logf("skipped %d slog.* calls with non-literal keys", dynamic)
	}
	if len(violations) == 0 {
		return
	}
	var lines []string
	for _, v := range violations {
		lines = append(lines, v.file+":"+strconv.Itoa(v.line)+" "+v.key)
	}
	t.Fatalf("slog attr keys not in AllowedKeys/DenyKeys:\n  %s", strings.Join(lines, "\n  "))
}
