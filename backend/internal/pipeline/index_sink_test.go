package pipeline

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
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
