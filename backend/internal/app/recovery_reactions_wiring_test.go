package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// This test pins the composition root. Recovery compiles and its unit tests pass
// even when the app forgets to connect the app-created ReactionAnalyzer, which
// would silently reintroduce stale dismissal reads after a process restart.
func TestProductionWiresReactionAnalyzerIntoCrashRecovery(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("app.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse app.go: %v", err)
	}

	var run *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == "Run" {
			run = fn
			break
		}
	}
	if run == nil {
		t.Fatal("Run function not found")
	}

	var analyzerCreated, recoveryWired, recoveryStarted token.Pos
	ast.Inspect(run.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if len(node.Lhs) != 1 || len(node.Rhs) != 1 {
				return true
			}
			lhs, ok := node.Lhs[0].(*ast.Ident)
			if !ok || lhs.Name != "reactionAnalyzer" {
				return true
			}
			call, ok := node.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if ok && sel.Sel.Name == "NewReactionAnalyzer" {
				analyzerCreated = node.Pos()
			}
		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				// Recovery runs via the periodic sweeper, which receives
				// orchestrator.RecoverIncomplete as its sweep function.
				fun, isIdent := node.Fun.(*ast.Ident)
				if !isIdent || fun.Name != "runPipelineRecoverySweeper" {
					return true
				}
				for _, arg := range node.Args {
					argSel, isSel := arg.(*ast.SelectorExpr)
					if !isSel {
						continue
					}
					argReceiver, _ := argSel.X.(*ast.Ident)
					if argReceiver != nil && argReceiver.Name == "orchestrator" && argSel.Sel.Name == "RecoverIncomplete" {
						recoveryStarted = node.Pos()
					}
				}
				return true
			}
			receiver, _ := sel.X.(*ast.Ident)
			switch sel.Sel.Name {
			case "SetRecoveryReactionReconciler":
				if receiver == nil || receiver.Name != "orchestrator" || len(node.Args) != 1 {
					return true
				}
				arg, ok := node.Args[0].(*ast.SelectorExpr)
				if !ok {
					return true
				}
				argReceiver, _ := arg.X.(*ast.Ident)
				if argReceiver != nil && argReceiver.Name == "reactionAnalyzer" && arg.Sel.Name == "SweepPRReactions" {
					recoveryWired = node.Pos()
				}
			case "RecoverIncomplete":
				if receiver != nil && receiver.Name == "orchestrator" {
					recoveryStarted = node.Pos()
				}
			}
		}
		return true
	})

	if analyzerCreated == token.NoPos {
		t.Fatal("production does not create ReactionAnalyzer")
	}
	if recoveryWired == token.NoPos {
		t.Fatal("production does not wire ReactionAnalyzer.SweepPRReactions into crash recovery")
	}
	if recoveryStarted == token.NoPos {
		t.Fatal("production does not start crash recovery")
	}
	if !(analyzerCreated < recoveryWired && recoveryWired < recoveryStarted) {
		t.Fatalf("production order is unsafe: analyzer=%d wiring=%d recovery=%d", analyzerCreated, recoveryWired, recoveryStarted)
	}
}
