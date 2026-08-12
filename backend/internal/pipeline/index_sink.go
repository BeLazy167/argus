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
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// memorySink is one post-review memory/index step: a feature-gated unit of
// indexing work (pattern learning, convention extraction, file-memory
// synthesis, PR/architecture summary indexing, architecture-graph extraction,
// PR-description enrichment). PostReviewIndexer.RunAll executes each sink in
// order under panic isolation.
type memorySink struct {
	// name labels the sink as the "op" in the panic log and telemetry event.
	name string
	// panicMsg overrides the recover log's message string for this sink. Empty
	// falls back to the cluster-derived "<stage> panic". Set it only to preserve
	// a sink's historical, grep-load-bearing message: enrichPRDescription logged
	// "enrichPRDescription panic" before the sink consolidation (#146 review
	// flagged the normalization to "post-review panic" as a grep regression).
	panicMsg string
	// enabled gates the sink on a per-run feature flag. nil means always-on.
	enabled func(run *PipelineRun) bool
	// run performs the indexing work. A panic here is recovered by RunAll and
	// isolated from sibling sinks; it must not be relied on for control flow.
	run func(ctx context.Context, run *PipelineRun, owner, repo string)
}

// memorySinkAuthority is the generation-ownership boundary for detached
// post-review sinks. *store.Store implements it with a PostgreSQL row lock so
// the authority check and an external mutation are ordered against a retry on
// another machine.
type memorySinkAuthority interface {
	IsReviewAttemptCurrent(ctx context.Context, reviewID uuid.UUID, generation int) (bool, error)
	RunIfReviewAttemptCurrent(ctx context.Context, reviewID uuid.UUID, generation int, write func(context.Context) error) (bool, error)
}

type memorySinkAttempt struct {
	authority  memorySinkAuthority
	reviewID   uuid.UUID
	generation int
	o          *Orchestrator
	stale      atomic.Bool
}

type memorySinkAttemptContextKey struct{}

// withMemorySinkAttempt installs the generation authority without acquiring it.
// Callers can finish arbitrary preparation first, then memorySinkWrite holds the
// review-row lock only across the mutation itself.
func withMemorySinkAttempt(ctx context.Context, o *Orchestrator, authority memorySinkAuthority, run *PipelineRun) (context.Context, *memorySinkAttempt) {
	attempt := &memorySinkAttempt{
		authority:  authority,
		reviewID:   run.ReviewID,
		generation: run.AttemptGeneration,
		o:          o,
	}
	return context.WithValue(ctx, memorySinkAttemptContextKey{}, attempt), attempt
}

// memorySinkWrite is the only mutation door for attempt-owned memory/index
// work. It does not add review ownership arguments to every sink: callers
// install the generation-owned attempt in context and wrap only the actual side
// effect (not slow LLM or batch preparation) with this function.
func memorySinkWrite(ctx context.Context, op string, write func(context.Context) error) (bool, error) {
	attempt, _ := ctx.Value(memorySinkAttemptContextKey{}).(*memorySinkAttempt)
	if attempt == nil || attempt.authority == nil {
		return true, write(ctx)
	}
	if attempt.stale.Load() {
		return false, nil
	}
	current, err := attempt.authority.RunIfReviewAttemptCurrent(ctx, attempt.reviewID, attempt.generation, write)
	if !current && err == nil {
		attempt.markStale(op)
	}
	return current, err
}

func (a *memorySinkAttempt) markStale(op string) {
	if a.stale.CompareAndSwap(false, true) {
		a.o.logger.Info("memory sink stopped: review attempt no longer current",
			"op", op, "review_id", a.reviewID, "attempt_generation", a.generation)
	}
}

// PostReviewIndexer runs post()'s post-review memory sink clusters. It owns the
// shared recover→emitPipelinePanicEvent isolation, generation ownership, and
// the feature-gating loop that post() used to hand-roll per sink.
type PostReviewIndexer struct {
	o         *Orchestrator
	authority memorySinkAuthority
}

// RunAll executes each enabled sink in order under panic isolation: a sink that
// panics is logged and emits a pipeline.panic_recovered event tagged with
// stage, and does NOT stop its siblings (post-review indexing is best-effort —
// the review is already composed). ctx is the cancel-detached context the sinks
// index under; stage is the telemetry stage label ("pre_post" or "post_review").
func (p *PostReviewIndexer) RunAll(ctx context.Context, run *PipelineRun, owner, repo, stage string, sinks []memorySink) {
	p.o.logger.InfoContext(ctx, "memory sink cluster started", "event", "pipeline.index.cluster_started", "review_id", run.ReviewID, "attempt_generation", run.AttemptGeneration, "stage", stage, "sink_count", len(sinks))
	sinkCtx, attempt := withMemorySinkAttempt(ctx, p.o, p.authority, run)
	for _, sink := range sinks {
		if sink.enabled != nil && !sink.enabled(run) {
			p.o.logger.DebugContext(ctx, "memory sink skipped by feature gate", "event", "pipeline.index.sink_skipped", "review_id", run.ReviewID, "stage", stage, "op", sink.name, "reason", "disabled")
			continue
		}
		if attempt.stale.Load() {
			p.o.logger.InfoContext(ctx, "memory sink cluster stopped for stale attempt", "event", "pipeline.index.cluster_stopped", "review_id", run.ReviewID, "stage", stage, "reason", "stale_attempt")
			return
		}
		if p.authority != nil {
			current, err := p.authority.IsReviewAttemptCurrent(sinkCtx, run.ReviewID, run.AttemptGeneration)
			if err != nil {
				// Authority storage failures are not evidence of staleness. Report
				// them and keep trying later sinks; completion remains best-effort.
				p.o.logger.Error("checking memory sink attempt authority",
					"error", err, "op", sink.name, "review_id", run.ReviewID,
					"attempt_generation", run.AttemptGeneration)
				continue
			}
			if !current {
				attempt.markStale(sink.name)
				return
			}
		}
		p.runSink(sinkCtx, run, owner, repo, stage, sink)
	}
	p.o.logger.InfoContext(ctx, "memory sink cluster completed", "event", "pipeline.index.cluster_completed", "review_id", run.ReviewID, "attempt_generation", run.AttemptGeneration, "stage", stage)
}

// runSink invokes one sink under a recover guard. Kept as its own method so the
// deferred recover pops per sink — a single defer inside RunAll's loop would
// unwind the whole loop on the first panic instead of isolating it.
func (p *PostReviewIndexer) runSink(ctx context.Context, run *PipelineRun, owner, repo, stage string, sink memorySink) {
	startedAt := time.Now()
	p.o.logger.InfoContext(ctx, "memory sink started", "event", "pipeline.index.sink_started", "review_id", run.ReviewID, "attempt_generation", run.AttemptGeneration, "stage", stage, "op", sink.name)
	defer func() {
		if r := recover(); r != nil {
			// Default message mirrors the historical per-cluster logs ("pre-post
			// panic" / "post-review panic") so log greps still match; a sink may
			// override it (sink.panicMsg) to keep its own historical message. The
			// emit-event stage keeps its underscore telemetry spelling
			// ("pre_post"/"post_review") regardless.
			msg := sink.panicMsg
			if msg == "" {
				msg = strings.ReplaceAll(stage, "_", "-") + " panic"
			}
			p.o.logger.Error(msg,
				"recover", r, "op", sink.name, "pr", run.PREvent.PRNumber)
			emitPipelinePanicEvent(ctx, p.o.logger, stage, r, run.TraceID)
		}
	}()
	sink.run(ctx, run, owner, repo)
	p.o.logger.InfoContext(ctx, "memory sink completed", "event", "pipeline.index.sink_completed", "review_id", run.ReviewID, "attempt_generation", run.AttemptGeneration, "stage", stage, "op", sink.name, "duration_ms", time.Since(startedAt).Milliseconds())
}
