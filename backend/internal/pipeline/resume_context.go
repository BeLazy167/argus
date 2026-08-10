// Package pipeline — resume_context.go re-resolves the run context that
// persistence throws away, for the one ingress that RELOADS a run instead of
// rebuilding it: StateMachine.Resume (mid-flight retry and crash recovery).
//
// PipelineRun keeps its resolved context on json:"-" fields, and
// pipeline_states stores only the JSON, so a run reloaded from that row comes
// back with every one of them at the zero value. The rebuild ingresses already
// re-resolve them (buildRun for a fresh review, buildRetryRun + the live
// re-resolution in RetryReview for a terminal one); the resume ingress did not,
// so a resumed review ran with:
//
//   - FeatureFlags zero — issue acceptance silently OFF, though it is ON by
//     default and the settings UI still renders it enabled
//   - Thresholds zero — every similarity gate at 0, so ScenarioTrigger accepts
//     ANY scenario match as a hit and ScenarioDedupe dedupes everything
//   - Indexer nil — the run posts its review but writes nothing back to memory
//   - Contract nil — depth, evidence bar, the security floor and the
//     unreviewable note all collapse to their defaults
//
// This is the same family of defect as the BudgetMaxFiles/BudgetNote loss,
// which was fixed by giving those two fields real json tags. That fix does not
// generalize here: Indexer is a live handle over the DB pool and cannot be
// serialized at all, and the sibling retry ingress deliberately re-resolves
// limits/thresholds from CURRENT settings rather than replaying the values the
// original attempt carried. One mechanism for all four, matching that ingress,
// beats persisting two of them and leaving two broken.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/pkg/diff"
)

// resumeHydrateTimeout bounds the two settings reads plus indexer resolution.
// Resume is on the crash-recovery path, which runs unattended at boot: a
// wedged DB read must not hold a recovered run out of its stage loop forever.
const resumeHydrateTimeout = 10 * time.Second

// resumeContextDeps is the narrow boundary the resume hydration consumes: the
// two per-install settings reads plus indexer resolution. Production composes
// *store.Store + the orchestrator's registry lookup via defaultResumeDeps; a
// test fake implements all three in a few lines (crosspr_stage_deps.go idiom).
type resumeContextDeps interface {
	GetMergedSettings(ctx context.Context, installationID, repoID int64) (json.RawMessage, error)
	// Same method — and the same never-hard-fails contract — that
	// featureFlagReader declares, so a resumeContextDeps value can be handed
	// straight to loadFeatureFlags.
	GetInstallationFeatureFlags(ctx context.Context, installationID int64) (json.RawMessage, error)
	ResolveIndexer(ctx context.Context, installationID int64) memory.Indexer
}

// hydrateResumeContext re-resolves the value-safe context a reloaded run lost:
// feature flags, similarity thresholds, the memory indexer, and the review
// contract. Best-effort in the same sense as the pre-review enrichers — a
// failed read leaves a resolved DEFAULT (never a zero value) and the run
// proceeds.
//
// DELIBERATELY NOT re-run here (the mid-flight decision issue #156 asks for):
// the enricher-backed context — PRIntent, SastFindings, ArchContext,
// LinkedIssues/LinkedPRs. Those cost a paid intent LLM call, a SAST pre-pass of
// up to 50 file fetches, and GitHub round trips, and crash recovery resumes
// automatically, so every restart would re-spend them. The line this function
// draws is between context that is WRONG when zero and context that is merely
// ABSENT: zero flags/thresholds/indexer silently change what the pipeline
// decides (a check turns off, a gate accepts everything, memory writes vanish),
// while a missing intent or SAST block only makes the remaining prompts
// thinner. The first class must be restored; the second can wait for the next
// full review.
func hydrateResumeContext(ctx context.Context, run *PipelineRun, dep resumeContextDeps, logger *slog.Logger) {
	if run == nil {
		return
	}
	hydrateCtx, cancel := context.WithTimeout(ctx, resumeHydrateTimeout)
	// Deferred, not cancelled between the reads: the pre-review link island
	// once cancelled its context before the feature-flag read, so flags silently
	// always fell back to defaults (see attachLinks).
	defer cancel()

	// loadFeatureFlags never fails — it falls back to DefaultFeatureFlags, which
	// is what a resumed run must carry rather than the all-off zero value.
	run.FeatureFlags = loadFeatureFlags(hydrateCtx, dep, run.DBInstallationID)

	if merged, err := dep.GetMergedSettings(hydrateCtx, run.DBInstallationID, run.DBRepoID); err == nil {
		run.Thresholds = parseThresholds(merged)
	} else {
		// Fixed-policy defaults, never the zero struct: a zero ScenarioTrigger
		// makes every scenario search hit "similar enough" to fire.
		run.Thresholds = memory.NewThresholds()
		logger.Error("resume: merged settings load failed, using default thresholds",
			"error", err, "installation", run.DBInstallationID, "repo", run.DBRepoID, "review_id", run.ReviewID)
	}

	// A nil indexer is legitimate (memory unconfigured for the org); resolving
	// here is what stops a resumed run on a CONFIGURED org from silently
	// writing nothing back. GetIndexer does not retain the context — it builds
	// a PGIndexer over the pool — so the bounded context above does not follow
	// the indexer into the stages that use it.
	run.Indexer = dep.ResolveIndexer(hydrateCtx, run.DBInstallationID)

	// The contract is RECOMPUTED, not re-enriched: ComputeContract is a pure
	// function of the persisted PR event + diff, so it costs nothing and cannot
	// double-charge anything. Only when still nil — hydration must never clobber
	// a contract the intent stage already resolved. What a rebuild cannot
	// restore is that LLM refinement, hence the signal: a rebuilt contract
	// carries the deterministic classification only, and Finalize will stamp an
	// unresolved class as production exactly as it does for a run whose intent
	// stage failed.
	if run.Contract == nil {
		var files []diff.FileDiff
		if run.Diff != nil {
			files = run.Diff.Files
		}
		run.Contract = ComputeContract(&run.PREvent, files)
		run.Contract.Signals = append(run.Contract.Signals, ContractSignalResumeRebuild)
	}

	logger.Info("resume: pipeline context rehydrated",
		"review_id", run.ReviewID, "state", run.State,
		"issue_acceptance", run.FeatureFlags.IssueAcceptance,
		"scenario_trigger", run.Thresholds.ScenarioTrigger,
		"memory", run.Indexer != nil,
		"change_class", run.Contract.ChangeClass)
}

// defaultResumeDeps composes the concrete store and the orchestrator's registry
// lookup into resumeContextDeps. Pure delegation — no business logic here.
type defaultResumeDeps struct {
	st      *store.Store
	indexer func(ctx context.Context, installationDBID int64) memory.Indexer
}

// errResumeStoreUnwired routes both settings reads to their default fallbacks
// when the store is absent, instead of panicking inside a method on a nil
// *store.Store. Wrapping the concrete pointer in this struct defeats
// loadFeatureFlags' nil-interface guard (the typed-nil hazard
// featureFlagReaderFor exists for), so the defusal lives here — same shape as
// defaultLinkDeps.
var errResumeStoreUnwired = errors.New("resume hydration: store unwired")

func (d defaultResumeDeps) GetMergedSettings(ctx context.Context, installationID, repoID int64) (json.RawMessage, error) {
	if d.st == nil {
		return nil, errResumeStoreUnwired
	}
	return d.st.GetMergedSettings(ctx, installationID, repoID)
}

func (d defaultResumeDeps) GetInstallationFeatureFlags(ctx context.Context, installationID int64) (json.RawMessage, error) {
	if d.st == nil {
		return nil, errResumeStoreUnwired
	}
	return d.st.GetInstallationFeatureFlags(ctx, installationID)
}

func (d defaultResumeDeps) ResolveIndexer(ctx context.Context, installationID int64) memory.Indexer {
	if d.indexer == nil {
		return nil
	}
	return d.indexer(ctx, installationID)
}

// hydrateResumedRun is the StateMachine.hydrate hook: it wires the orchestrator's
// store and memory registry into hydrateResumeContext. Kept as a method so
// NewOrchestrator can hand the state machine one function value.
func (o *Orchestrator) hydrateResumedRun(ctx context.Context, run *PipelineRun) {
	hydrateResumeContext(ctx, run, defaultResumeDeps{st: o.st, indexer: o.resolveIndexer}, o.logger)
}
