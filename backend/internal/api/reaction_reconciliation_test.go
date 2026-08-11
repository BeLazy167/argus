package api

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/inflight"
	"github.com/BeLazy167/argus/backend/internal/pipeline"
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

type blockingReactionSweeper struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingReactionSweeper) SweepPRReactions(context.Context, int64, string, int) error {
	close(s.started)
	<-s.release
	return nil
}

func newLaunchBarrierTestServer(sweeper reactionSweeper, handler prEventHandler) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Server{
		reactionSweeper: sweeper,
		prEventHandler:  handler,
		launcher:        pipeline.NewLauncher(inflight.NewRegistry(), nil, nil, logger),
	}
}

func TestLaunchPREventRunsPathAdmissionBeforeSynchronousReactionSweep(t *testing.T) {
	sweeper := &blockingReactionSweeper{started: make(chan struct{}), release: make(chan struct{})}
	handled := make(chan struct{})
	s := newLaunchBarrierTestServer(sweeper, &recordingPREventHandler{onHandle: func() { close(handled) }})
	var mu sync.Mutex
	order := make([]string, 0, 2)
	launchResult := make(chan error, 1)
	evt := ghpkg.PREvent{InstallationID: 91, RepoFullName: "acme/api", PRNumber: 17}

	go func() {
		launchResult <- s.launchPREvent(pipeline.LaunchSpec{
			Repo: evt.RepoFullName, PR: evt.PRNumber, BaseCtx: context.Background(),
			BeforeSpawn: func(context.Context) error {
				mu.Lock()
				order = append(order, "admit")
				mu.Unlock()
				return nil
			},
		}, evt)
	}()

	<-sweeper.started
	mu.Lock()
	order = append(order, "sweep")
	gotOrder := append([]string(nil), order...)
	mu.Unlock()
	if len(gotOrder) != 2 || gotOrder[0] != "admit" || gotOrder[1] != "sweep" {
		close(sweeper.release)
		t.Fatalf("launch barrier order = %v, want [admit sweep]", gotOrder)
	}
	select {
	case err := <-launchResult:
		close(sweeper.release)
		t.Fatalf("launch returned before reaction reconciliation completed: %v", err)
	default:
	}
	select {
	case <-handled:
		close(sweeper.release)
		t.Fatal("pipeline spawned while reaction reconciliation was blocked")
	default:
	}

	close(sweeper.release)
	if err := <-launchResult; err != nil {
		t.Fatalf("launchPREvent: %v", err)
	}
	<-handled
}

func TestLaunchPREventPathRefusalSkipsSweepAndDoesNotCleanup(t *testing.T) {
	admissionErr := errors.New("review actor lacks write access")
	sweeps := 0
	cleanups := 0
	handled := false
	s := newLaunchBarrierTestServer(
		&recordingReactionSweeper{onSweep: func() { sweeps++ }},
		&recordingPREventHandler{onHandle: func() { handled = true }},
	)
	evt := ghpkg.PREvent{InstallationID: 91, RepoFullName: "acme/api", PRNumber: 17}

	err := s.launchPREvent(pipeline.LaunchSpec{
		Repo: evt.RepoFullName, PR: evt.PRNumber, BaseCtx: context.Background(),
		BeforeSpawn: func(context.Context) error { return admissionErr },
		Cleanup:     func() { cleanups++ },
	}, evt)
	if !errors.Is(err, admissionErr) {
		t.Fatalf("launch error = %v, want path admission failure", err)
	}
	if sweeps != 0 {
		t.Fatalf("reaction sweeps = %d, want zero for refused path", sweeps)
	}
	if cleanups != 0 {
		t.Fatalf("cleanup calls = %d, want zero because the path owns its own error cleanup", cleanups)
	}
	if handled {
		t.Fatal("pipeline spawned after path admission failed")
	}
}

func TestLaunchPREventSweepFailureCleansSuccessfulPathExactlyOnce(t *testing.T) {
	sweepErr := errors.New("GitHub 503 service unavailable")
	beforeSpawn := 0
	cleanups := 0
	handled := false
	s := newLaunchBarrierTestServer(
		&recordingReactionSweeper{err: sweepErr},
		&recordingPREventHandler{onHandle: func() { handled = true }},
	)
	evt := ghpkg.PREvent{InstallationID: 91, RepoFullName: "acme/api", PRNumber: 17}

	err := s.launchPREvent(pipeline.LaunchSpec{
		Repo: evt.RepoFullName, PR: evt.PRNumber, BaseCtx: context.Background(),
		BeforeSpawn: func(context.Context) error {
			beforeSpawn++
			return nil
		},
		Cleanup: func() { cleanups++ },
	}, evt)
	if !errors.Is(err, sweepErr) {
		t.Fatalf("launch error = %v, want wrapped sweep failure", err)
	}
	if beforeSpawn != 1 {
		t.Fatalf("path BeforeSpawn calls = %d, want one", beforeSpawn)
	}
	if cleanups != 1 {
		t.Fatalf("cleanup calls = %d, want exactly one after admitted path cannot spawn", cleanups)
	}
	if handled {
		t.Fatal("pipeline spawned after reconciliation failed")
	}
}

func TestRetryReactionSweepIsInsideSynchronousBeforeSpawn(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, filepath.Join(dir, "handlers_reviews.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var beforeSpawnReconciles, asyncRunReconciles bool
	ast.Inspect(parsed, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "retryReview" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || (key.Name != "BeforeSpawn" && key.Name != "Run") {
					continue
				}
				ast.Inspect(kv.Value, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if ok && sel.Sel.Name == "reconcileReactionsBeforeReview" {
						if key.Name == "BeforeSpawn" {
							beforeSpawnReconciles = true
						} else {
							asyncRunReconciles = true
						}
					}
					return true
				})
			}
			return true
		})
		return false
	})
	if !beforeSpawnReconciles {
		t.Fatal("retry review does not reconcile reactions in synchronous BeforeSpawn")
	}
	if asyncRunReconciles {
		t.Fatal("retry review defers reaction reconciliation to asynchronous Run")
	}
}
