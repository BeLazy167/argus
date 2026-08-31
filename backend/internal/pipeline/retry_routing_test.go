// Package pipeline — retry_routing_test.go pins how RetryReview reconstitutes
// a persisted non-terminal run. Persisting at triaging is what makes a triage
// crash visible to recovery, but it also silently reroutes retries of those
// crashes onto the resume path — which keeps neither the pre-review enrichers
// (all json:"-") nor a fresh diff and head SHA. RetryReview needs a database
// and a GitHub client, so the routing is pinned structurally, the same way
// app.go's recovery wiring is pinned in internal/app.
package pipeline

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestRetryReviewRoutesTriageToRebuild(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "orchestrator.go", nil, 0)
	if err != nil {
		t.Fatalf("parse orchestrator.go: %v", err)
	}

	var retry *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "RetryReview" {
			retry = fn
			break
		}
	}
	if retry == nil {
		t.Fatal("RetryReview not found")
	}

	var triageGuard, rebuildCall, resumeCall token.Pos
	ast.Inspect(retry.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BinaryExpr:
			// Looking for a comparison against StateTriaging.
			if sel, ok := node.Y.(*ast.Ident); ok && sel.Name == "StateTriaging" && triageGuard == token.NoPos {
				triageGuard = node.Pos()
			}
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "retryFromReviewRow":
				if rebuildCall == token.NoPos {
					rebuildCall = node.Pos()
				}
			case "ResumeAttempt":
				if resumeCall == token.NoPos {
					resumeCall = node.Pos()
				}
			}
		}
		return true
	})

	if triageGuard == token.NoPos {
		t.Fatal("RetryReview does not branch on StateTriaging — a triage-crash retry would resume, losing the pre-review enrichers and reviewing a stale diff against stale SHAs")
	}
	if rebuildCall == token.NoPos {
		t.Fatal("RetryReview never calls retryFromReviewRow")
	}
	if resumeCall == token.NoPos {
		t.Fatal("RetryReview never calls ResumeAttempt")
	}
	// The triage guard must come BEFORE the in-place resume, or it can never
	// take effect for a non-terminal triaging run.
	if !(triageGuard < resumeCall) {
		t.Fatalf("triage guard at %d must precede the resume at %d", triageGuard, resumeCall)
	}
}
