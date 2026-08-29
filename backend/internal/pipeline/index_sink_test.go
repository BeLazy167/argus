package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/google/uuid"
)

// newSinkOrchestrator builds a minimal Orchestrator whose logger writes to buf —
// enough to exercise PostReviewIndexer.RunAll's isolation + gating without the
// full pipeline wiring.
func newSinkOrchestrator(buf *bytes.Buffer) *Orchestrator {
	return &Orchestrator{logger: slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelError}))}
}

// TestRunAllPanicIsolation locks the core contract: a sink that panics is
// recovered so its siblings still run, logs a "<stage> panic" line carrying its
// op, and emits the pipeline.panic_recovered telemetry event tagged with stage.
func TestRunAllPanicIsolation(t *testing.T) {
	var buf bytes.Buffer
	o := newSinkOrchestrator(&buf)
	run := &PipelineRun{PREvent: ghpkg.PREvent{PRNumber: 42}, TraceID: "trace-xyz"}

	var order []string
	sinks := []memorySink{
		{name: "first", run: func(ctx context.Context, r *PipelineRun, owner, repo string) { order = append(order, "first") }},
		{name: "boom", run: func(ctx context.Context, r *PipelineRun, owner, repo string) {
			order = append(order, "boom")
			panic("kaboom")
		}},
		{name: "third", run: func(ctx context.Context, r *PipelineRun, owner, repo string) { order = append(order, "third") }},
	}

	o.indexer().RunAll(context.Background(), run, "octo", "repo", "pre_post", sinks)

	// Sibling isolation: the panic in "boom" must not stop "third".
	if got := strings.Join(order, ","); got != "first,boom,third" {
		t.Fatalf("sink order = %q, want first,boom,third (a panicking sink must not stop siblings)", got)
	}
	logs := buf.String()
	if !strings.Contains(logs, "pre-post panic") {
		t.Errorf("missing per-sink panic log line (grep-compat message):\n%s", logs)
	}
	if !strings.Contains(logs, "op=boom") {
		t.Errorf("panic log missing op=boom:\n%s", logs)
	}
	if !strings.Contains(logs, "pipeline.panic_recovered") || !strings.Contains(logs, "stage=pre_post") {
		t.Errorf("missing panic_recovered telemetry event tagged stage=pre_post:\n%s", logs)
	}
}

// TestRunAllGating locks feature-gating: enabled()==false skips the sink,
// enabled()==true runs it, and nil enabled is always-on.
func TestRunAllGating(t *testing.T) {
	var buf bytes.Buffer
	o := newSinkOrchestrator(&buf)
	run := &PipelineRun{PREvent: ghpkg.PREvent{PRNumber: 7}}

	var ran []string
	mark := func(name string) func(context.Context, *PipelineRun, string, string) {
		return func(ctx context.Context, r *PipelineRun, owner, repo string) { ran = append(ran, name) }
	}
	sinks := []memorySink{
		{name: "off", enabled: func(r *PipelineRun) bool { return false }, run: mark("off")},
		{name: "on", enabled: func(r *PipelineRun) bool { return true }, run: mark("on")},
		{name: "always", run: mark("always")},
	}
	o.indexer().RunAll(context.Background(), run, "o", "r", "pre_post", sinks)

	if got := strings.Join(ran, ","); got != "on,always" {
		t.Fatalf("ran = %q, want on,always (disabled sink skipped, nil-enabled always-on)", got)
	}
}

// TestRunAllPostReviewStageMessage locks the stage→message mapping for the other
// cluster: post_review renders the "post-review panic" grep string while the
// emitted event keeps the underscore telemetry spelling.
func TestRunAllPostReviewStageMessage(t *testing.T) {
	var buf bytes.Buffer
	o := newSinkOrchestrator(&buf)
	run := &PipelineRun{PREvent: ghpkg.PREvent{PRNumber: 9}, TraceID: "t"}

	o.indexer().RunAll(context.Background(), run, "o", "r", "post_review", []memorySink{
		{name: "extractArchitectureGraph", run: func(ctx context.Context, r *PipelineRun, owner, repo string) { panic("graph exploded") }},
	})

	logs := buf.String()
	if !strings.Contains(logs, "post-review panic") {
		t.Errorf("missing 'post-review panic' grep-compat message:\n%s", logs)
	}
	if !strings.Contains(logs, "stage=post_review") {
		t.Errorf("telemetry event missing stage=post_review:\n%s", logs)
	}
}

type fakeMemorySinkAuthority struct {
	mu         sync.Mutex
	generation int
	checkErr   error
}

func (f *fakeMemorySinkAuthority) IsReviewAttemptCurrent(_ context.Context, _ uuid.UUID, generation int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checkErr != nil {
		return false, f.checkErr
	}
	return generation == f.generation, nil
}

func (f *fakeMemorySinkAuthority) RunIfReviewAttemptCurrent(ctx context.Context, _ uuid.UUID, generation int, write func(context.Context) error) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if generation != f.generation {
		return false, nil
	}
	return true, write(ctx)
}

func (f *fakeMemorySinkAuthority) advance(generation int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generation = generation
}

// TestRunAllGenerationOwnership covers the cross-machine retry race: attempt 1
// enters slow preparation while current, attempt 2 advances the generation,
// and the old attempt reaches its first mutation only afterward. None of the
// old pattern/convention/file/PR/architecture writes may run; the current
// attempt must still run every sink.
func TestRunAllGenerationOwnership(t *testing.T) {
	var logs bytes.Buffer
	authority := &fakeMemorySinkAuthority{generation: 1}
	o := newSinkOrchestrator(&logs)
	o.sinkAuthority = authority
	reviewID := uuid.New()

	enteredSlow := make(chan struct{})
	releaseSlow := make(chan struct{})
	var writesMu sync.Mutex
	var writes []string
	var errMu sync.Mutex
	var runErr error

	names := []string{
		"autoLearnPatterns",
		"learnPositivePatterns",
		"extractConventions",
		"synthesizeFileMemories",
		"indexPRSummary",
		"indexArchitectureSummary",
		"extractArchitectureGraph",
	}
	makeSinks := func() []memorySink {
		sinks := make([]memorySink, 0, len(names))
		for i, name := range names {
			i, name := i, name
			sinks = append(sinks, memorySink{name: name, run: func(ctx context.Context, run *PipelineRun, _, _ string) {
				if run.AttemptGeneration == 1 && i == 0 {
					close(enteredSlow)
					<-releaseSlow
				}
				authorized, err := memorySinkWrite(ctx, name+".write", func(context.Context) error {
					writesMu.Lock()
					defer writesMu.Unlock()
					writes = append(writes, fmt.Sprintf("%d:%s", run.AttemptGeneration, name))
					return nil
				})
				if err != nil || (run.AttemptGeneration == 2 && !authorized) {
					errMu.Lock()
					defer errMu.Unlock()
					if err != nil {
						runErr = errors.Join(runErr, err)
					}
					if run.AttemptGeneration == 2 && !authorized {
						runErr = errors.Join(runErr, fmt.Errorf("current sink %s was not authorized", name))
					}
				}
			}})
		}
		return sinks
	}

	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		o.indexer().RunAll(context.Background(), &PipelineRun{ReviewID: reviewID, AttemptGeneration: 1}, "o", "r", "pre_post", makeSinks())
	}()
	<-enteredSlow

	// This models BeginReviewRetry committing on another machine while the old
	// attempt is still doing slow, mutation-free LLM preparation.
	authority.advance(2)
	o.indexer().RunAll(context.Background(), &PipelineRun{ReviewID: reviewID, AttemptGeneration: 2}, "o", "r", "pre_post", makeSinks())
	close(releaseSlow)
	<-oldDone

	errMu.Lock()
	gotErr := runErr
	errMu.Unlock()
	if gotErr != nil {
		t.Fatal(gotErr)
	}
	writesMu.Lock()
	defer writesMu.Unlock()
	if len(writes) != len(names) {
		t.Fatalf("writes = %v, want exactly %d current-attempt writes", writes, len(names))
	}
	for i, name := range names {
		want := "2:" + name
		if writes[i] != want {
			t.Fatalf("writes[%d] = %q, want %q (all old-attempt writes must be fenced)", i, writes[i], want)
		}
	}
}

func TestRunAllAuthorityStorageErrorIsReportedAndDoesNotRunSink(t *testing.T) {
	var logs bytes.Buffer
	o := newSinkOrchestrator(&logs)
	o.sinkAuthority = &fakeMemorySinkAuthority{generation: 1, checkErr: errors.New("database unavailable")}
	run := &PipelineRun{ReviewID: uuid.New(), AttemptGeneration: 1}
	ran := false

	o.indexer().RunAll(context.Background(), run, "o", "r", "pre_post", []memorySink{{
		name: "indexPRSummary",
		run:  func(context.Context, *PipelineRun, string, string) { ran = true },
	}})

	if ran {
		t.Fatal("sink ran without a successful generation authority check")
	}
	if got := logs.String(); !strings.Contains(got, "checking memory sink attempt authority") || !strings.Contains(got, "database unavailable") {
		t.Fatalf("authority storage error was silently skipped:\n%s", got)
	}
}
