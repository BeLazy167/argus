// Package pipeline — index_sink.go collapses post()'s post-review memory/index
// clusters into declarative sink lists run under one panic-isolation loop.
//
// Before this file, post() open-coded ~8 identical `defer recover →
// emitPipelinePanicEvent` + feature-gate closures around the pre-post and
// post-review indexing calls. Each is load-bearing (a panic in one indexer must
// never abort the others or the completion write) but was copy-pasted, so a fix
// to the isolation shape had to land in eight places. PostReviewIndexer.RunAll
// owns that recover-wrap and gating loop once; post() just declares which sinks
// run in which cluster.
package pipeline

import (
	"context"
	"strings"
)

// memorySink is one post-review memory/index step: a feature-gated unit of
// indexing work (pattern learning, convention extraction, file-memory
// synthesis, PR/architecture summary indexing, architecture-graph extraction,
// PR-description enrichment). PostReviewIndexer.RunAll executes each sink in
// order under panic isolation.
type memorySink struct {
	// name labels the sink as the "op" in the panic log and telemetry event.
	name string
	// enabled gates the sink on a per-run feature flag. nil means always-on.
	enabled func(run *PipelineRun) bool
	// run performs the indexing work. A panic here is recovered by RunAll and
	// isolated from sibling sinks; it must not be relied on for control flow.
	run func(ctx context.Context, run *PipelineRun, owner, repo string)
}

// PostReviewIndexer runs post()'s post-review memory sink clusters. It owns the
// shared recover→emitPipelinePanicEvent isolation and the feature-gating loop
// that post() used to hand-roll per sink.
type PostReviewIndexer struct {
	o *Orchestrator
}

// RunAll executes each enabled sink in order under panic isolation: a sink that
// panics is logged and emits a pipeline.panic_recovered event tagged with
// stage, and does NOT stop its siblings (post-review indexing is best-effort —
// the review is already composed). ctx is the cancel-detached context the sinks
// index under; stage is the telemetry stage label ("pre_post" or "post_review").
func (p *PostReviewIndexer) RunAll(ctx context.Context, run *PipelineRun, owner, repo, stage string, sinks []memorySink) {
	for _, sink := range sinks {
		if sink.enabled != nil && !sink.enabled(run) {
			continue
		}
		p.runSink(ctx, run, owner, repo, stage, sink)
	}
}

// runSink invokes one sink under a recover guard. Kept as its own method so the
// deferred recover pops per sink — a single defer inside RunAll's loop would
// unwind the whole loop on the first panic instead of isolating it.
func (p *PostReviewIndexer) runSink(ctx context.Context, run *PipelineRun, owner, repo, stage string, sink memorySink) {
	defer func() {
		if r := recover(); r != nil {
			// Message mirrors the historical per-sink logs ("pre-post panic" /
			// "post-review panic") so log greps still match; the emit-event stage
			// keeps its underscore telemetry spelling ("pre_post"/"post_review").
			p.o.logger.Error(strings.ReplaceAll(stage, "_", "-")+" panic",
				"recover", r, "op", sink.name, "pr", run.PREvent.PRNumber)
			emitPipelinePanicEvent(ctx, p.o.logger, stage, r, run.TraceID)
		}
	}()
	sink.run(ctx, run, owner, repo)
}
