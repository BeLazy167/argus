package api

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sync"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
)

type recordingReactionSweeper struct {
	err     error
	onSweep func()
}

func (s *recordingReactionSweeper) SweepPRReactions(context.Context, int64, string, int) error {
	if s.onSweep != nil {
		s.onSweep()
	}
	return s.err
}

type recordingPREventHandler struct {
	onHandle func()
}

func (h *recordingPREventHandler) HandlePREvent(context.Context, ghpkg.PREvent) error {
	if h.onHandle != nil {
		h.onHandle()
	}
	return nil
}

func TestRunPREventReconcilesReactionsBeforePipeline(t *testing.T) {
	order := make([]string, 0, 2)
	var mu sync.Mutex
	sweeper := &recordingReactionSweeper{onSweep: func() {
		mu.Lock()
		order = append(order, "sweep")
		mu.Unlock()
	}}
	handler := &recordingPREventHandler{onHandle: func() {
		mu.Lock()
		order = append(order, "handle")
		mu.Unlock()
	}}
	s := &Server{reactionSweeper: sweeper, prEventHandler: handler}

	evt := ghpkg.PREvent{InstallationID: 91, RepoFullName: "acme/api", PRNumber: 17}
	if err := s.runPREvent(context.Background(), evt); err != nil {
		t.Fatalf("runPREvent: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "sweep" || order[1] != "handle" {
		t.Fatalf("review order = %v, want [sweep handle]", order)
	}
}

func TestRunPREventSweepFailureBlocksPipeline(t *testing.T) {
	sweepErr := errors.New("github reactions unavailable")
	sweeper := &recordingReactionSweeper{err: sweepErr}
	handled := false
	handler := &recordingPREventHandler{onHandle: func() { handled = true }}
	s := &Server{reactionSweeper: sweeper, prEventHandler: handler}

	err := s.runPREvent(context.Background(), ghpkg.PREvent{
		InstallationID: 91, RepoFullName: "acme/api", PRNumber: 17,
	})
	if !errors.Is(err, sweepErr) {
		t.Fatalf("runPREvent error = %v, want wrapped sweep error", err)
	}
	if handled {
		t.Fatal("HandlePREvent ran after an incomplete reaction sweep; stale dismissal trust was available")
	}
}

func TestRunPREventRemovedThumbsDownCannotSuppressNextReview(t *testing.T) {
	// Model the production boundary: reaction feedback starts as a live
	// dismissal, the sweep observes that the thumbs-down is gone and retracts
	// it, then HandlePREvent performs dismissal retrieval.
	dismissalLive := true
	suppressed := false
	s := &Server{
		reactionSweeper: &recordingReactionSweeper{onSweep: func() { dismissalLive = false }},
		prEventHandler:  &recordingPREventHandler{onHandle: func() { suppressed = dismissalLive }},
	}

	if err := s.runPREvent(context.Background(), ghpkg.PREvent{
		InstallationID: 91, RepoFullName: "acme/api", PRNumber: 17,
	}); err != nil {
		t.Fatalf("runPREvent: %v", err)
	}
	if suppressed {
		t.Fatal("removed thumbs-down still suppressed the next review")
	}
}

// TestPREventLaunchPathsUseReconciledRunner pins the production call sites.
// Each fresh-review entry point must route through launchPREvent; calling the
// launcher directly would bypass the mandatory reaction sweep.
func TestPREventLaunchPathsUseReconciledRunner(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve package directory: %v", err)
	}
	tests := []struct {
		name string
		file string
		fn   string
	}{
		{name: "pull_request auto review", file: "handlers_webhook.go", fn: "handleWebhook"},
		{name: "argus review command", file: "commands.go", fn: "handleReviewCommand"},
		{name: "checkbox trigger", file: "handlers_webhook.go", fn: "handleCheckboxTrigger"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, filepath.Join(dir, tt.file), nil, 0)
			if err != nil {
				t.Fatalf("parse production file: %v", err)
			}
			var body *ast.BlockStmt
			for _, decl := range parsed.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Name.Name == tt.fn {
					body = fn.Body
					break
				}
			}
			if body == nil {
				t.Fatalf("production function %s not found", tt.fn)
			}

			var reconciledLaunch, directHandle bool
			ast.Inspect(body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "launchPREvent":
					reconciledLaunch = true
				case "HandlePREvent":
					directHandle = true
				}
				return true
			})
			if !reconciledLaunch {
				t.Fatalf("%s does not use launchPREvent", tt.fn)
			}
			if directHandle {
				t.Fatalf("%s directly calls HandlePREvent and can bypass reaction reconciliation", tt.fn)
			}
		})
	}
}
