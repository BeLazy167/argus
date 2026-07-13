// Package pipeline — incremental.go owns the single "is this push an
// incremental re-review, and what are its priors?" decision.
//
// The decision used to be inlined four times in orchestrator.go: the fresh
// webhook path, the no-run retry path, buildRetryRun, and — separately —
// autoResolveOnSynchronize. The last two both fetched the SAME
// GetLastCompletedReview + GetCompareCommitsDiff pair, so the inter-diff
// GitHub round-trip fired TWICE per synchronize. IncrementalResolver.Resolve
// computes it ONCE; HandlePREvent shares the resulting plan with the
// auto-resolve goroutine and the review path, and the retry path calls Resolve
// for its priors.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/pkg/diff"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// IncrementalPlan is the resolved re-review decision for one (repo, PR, push).
//
// IsIncremental is true only when a usable inter-diff patch was produced;
// consumers that see it override the full-PR diff with InterDiffPatch. Fallback
// is the counterpart: a prior review existed (so an inter-diff was expected) but
// couldn't be produced — a fetch error, an empty compare (force-push / base
// change), or a parse failure. It is set independently of IsIncremental so a
// caller can fall back to a full review while still surfacing that it did.
//
// PriorComments aggregate unresolved findings across ALL completed reviews on
// the PR (not only the most-recent), so incremental dedup and the no-run retry
// path see every prior comment. PreviousReviewID points at the single
// most-recent completed review (the "previous" pointer persisted on the run).
type IncrementalPlan struct {
	IsIncremental    bool
	PreviousReviewID *uuid.UUID
	PriorComments    map[string][]PriorComment
	InterDiffPatch   *diff.PatchSet
	InterDiffRaw     string
	Fallback         bool
	FallbackReason   string
}

// incrementalStore is the narrow store surface Resolve needs. *store.Store
// satisfies it; tests supply an in-memory fake.
type incrementalStore interface {
	GetLastCompletedReview(ctx context.Context, repoID int64, prNumber int) (*store.Review, error)
	GetPRCompletedReviewComments(ctx context.Context, repoID int64, prNumber int) ([]store.ReviewComment, error)
}

// interDiffFetcher is the narrow GitHub surface Resolve needs. *ghpkg.Client
// satisfies it; tests supply an in-memory fake.
type interDiffFetcher interface {
	GetCompareCommitsDiff(ctx context.Context, installationID int64, owner, repo, base, head string) (string, error)
}

// IncrementalResolver resolves the incremental re-review plan for a push. It
// holds only the two boundaries the decision crosses (store + GitHub) plus a
// logger, so it is trivially testable against fakes.
type IncrementalResolver struct {
	store  incrementalStore
	gh     interDiffFetcher
	logger *slog.Logger
}

// NewIncrementalResolver wires the resolver. logger may be nil (falls back to a
// discard logger) so tests can construct it without observability plumbing.
func NewIncrementalResolver(st incrementalStore, gh interDiffFetcher, logger *slog.Logger) *IncrementalResolver {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &IncrementalResolver{store: st, gh: gh, logger: logger}
}

// ResolvePriors returns ONLY the prior-review context — the most-recent
// completed review pointer plus priors aggregated across ALL completed reviews
// on the PR — with NO inter-diff fetch and NO fallback telemetry.
//
// The no-run retry path uses it: a retry does a full re-review off the whole PR
// diff, so it needs priors for dedup but must not pay a GetCompareCommitsDiff
// round-trip it would discard, nor emit an "incremental.fallback" signal — a
// retry is not a push, and an empty/errored compare there would falsely pollute
// the force-push/base-change signal (and its ERROR-level parse alarm).
func (r *IncrementalResolver) ResolvePriors(ctx context.Context, repoID int64, prNumber int) *IncrementalPlan {
	_, plan := r.loadPriors(ctx, repoID, prNumber)
	return plan
}

// loadPriors loads the previous-review pointer and aggregated priors — the part
// of the decision shared by Resolve (push path) and ResolvePriors (retry path).
// Returns the prev review so Resolve can compute the inter-diff against its
// HeadSHA; prev is nil (with an empty plan) when there is no prior completed
// review or the load failed.
func (r *IncrementalResolver) loadPriors(ctx context.Context, repoID int64, prNumber int) (*store.Review, *IncrementalPlan) {
	plan := &IncrementalPlan{}
	prev, err := r.store.GetLastCompletedReview(ctx, repoID, prNumber)
	if err != nil {
		// pgx.ErrNoRows is the ordinary first-push case — quiet. Any other error
		// is a DB blip, not a force-push, so it is NOT a fallback signal: log it
		// and treat the push as a plain full review.
		if !errors.Is(err, pgx.ErrNoRows) {
			r.logger.WarnContext(ctx, "incremental: load prior review", "error", err, "pr", prNumber)
		}
		return nil, plan
	}
	if prev == nil {
		return nil, plan
	}
	plan.PreviousReviewID = &prev.ID
	plan.PriorComments = r.aggregatePriorComments(ctx, repoID, prNumber)
	return prev, plan
}

// Resolve computes the re-review plan for the (repoID, event.PRNumber) push.
//
// repoID is the Argus DB repos.id; event carries PRNumber, RepoFullName (→
// owner/repo), InstallationID, and HeadSHA. Never returns nil: an empty plan
// means "no prior completed review — full review, no fallback signal" (the
// legitimate first-push case, distinct from a fallback).
//
// The inter-diff is fetched at most once per call, so a caller that shares one
// plan across the review path and the auto-resolve goroutine pays a single
// GitHub round-trip per push.
func (r *IncrementalResolver) Resolve(ctx context.Context, repoID int64, event ghpkg.PREvent) *IncrementalPlan {
	prev, plan := r.loadPriors(ctx, repoID, event.PRNumber)
	if prev == nil {
		return plan
	}

	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		// Can't compute an inter-diff without a valid owner/repo. A prior review
		// exists but we can't compare against it — signal the fallback.
		r.signalFallback(ctx, plan, "bad_repo_name", prev.HeadSHA, event, err)
		return plan
	}

	interDiff, err := r.gh.GetCompareCommitsDiff(ctx, event.InstallationID, owner, repo, prev.HeadSHA, event.HeadSHA)
	if err != nil {
		r.signalFallback(ctx, plan, "inter_diff_fetch_failed", prev.HeadSHA, event, err)
		return plan
	}
	if interDiff == "" {
		// Same commit already reviewed (e.g. a retry with no intervening push, or
		// a replayed webhook) ⇒ nothing to diff, and no anomaly to surface.
		if prev.HeadSHA == event.HeadSHA {
			return plan
		}
		// Empty compare between two DIFFERENT commits ⇒ force-push or base change
		// rewrote history. Not an error, but a costly full review with no trace
		// is exactly what this signal exists to prevent.
		r.signalFallback(ctx, plan, "empty_inter_diff", prev.HeadSHA, event, nil)
		return plan
	}

	interPatch, err := diff.Parse(interDiff)
	if err != nil {
		// GitHub's own diff being unparseable means our parser regressed —
		// signalFallback logs parse failures at Error level.
		r.signalFallback(ctx, plan, "inter_diff_parse_failed", prev.HeadSHA, event, err)
		return plan
	}

	plan.IsIncremental = true
	plan.InterDiffPatch = interPatch
	plan.InterDiffRaw = interDiff
	r.logger.InfoContext(ctx, "incremental re-review",
		"previous_review_id", prev.ID,
		"previous_head", prev.HeadSHA,
		"new_head", event.HeadSHA,
		"pr", event.PRNumber)
	return plan
}

// aggregatePriorComments loads and dedupes prior comments across all completed
// reviews on the PR. Returns nil when there are none.
func (r *IncrementalResolver) aggregatePriorComments(ctx context.Context, repoID int64, prNumber int) map[string][]PriorComment {
	comments, err := r.store.GetPRCompletedReviewComments(ctx, repoID, prNumber)
	if err != nil {
		r.logger.WarnContext(ctx, "incremental: load prior comments", "error", err, "pr", prNumber)
		return nil
	}
	return buildPriorComments(dedupeReviewComments(comments))
}

// signalFallback marks the plan as a full-review fallback and SURFACES it as a
// named observability event (not a bare Warn): the PostHog slog handler
// promotes any record carrying an "event" attr into a capture, so a
// force-push / base-change that silently turns an incremental re-review into a
// costly full run leaves a trace. A parse failure is a parser regression and
// logs at Error; the rest are operational and log at Warn.
func (r *IncrementalResolver) signalFallback(ctx context.Context, plan *IncrementalPlan, reason, prevHead string, event ghpkg.PREvent, cause error) {
	plan.Fallback = true
	plan.FallbackReason = reason
	attrs := []any{
		slog.String("event", "incremental.fallback"),
		slog.String("reason", reason),
		slog.String("previous_head", prevHead),
		slog.String("new_head", event.HeadSHA),
		slog.String("repo", event.RepoFullName),
		slog.Int("pr_number", event.PRNumber),
	}
	if cause != nil {
		// Untyped key/value pair (not slog.Any): error strings can carry
		// arbitrary content, so "error" stays off the PostHog typed-attr
		// allowlist — this matches the codebase's variadic logging idiom.
		attrs = append(attrs, "error", cause)
	}
	msg := fmt.Sprintf("incremental fallback to full review: %s", reason)
	if reason == "inter_diff_parse_failed" {
		r.logger.ErrorContext(ctx, msg, attrs...)
		return
	}
	r.logger.WarnContext(ctx, msg, attrs...)
}

// dedupeReviewComments drops comments duplicated across the PR's completed
// reviews (same file, range, and body), keeping first occurrence. Aggregating
// across all reviews re-surfaces a finding flagged on every push otherwise.
func dedupeReviewComments(comments []store.ReviewComment) []store.ReviewComment {
	if len(comments) == 0 {
		return comments
	}
	seen := make(map[string]struct{}, len(comments))
	out := comments[:0:0]
	for _, c := range comments {
		start, end := 0, 0
		if c.StartLine != nil {
			start = *c.StartLine
		}
		if c.EndLine != nil {
			end = *c.EndLine
		}
		key := fmt.Sprintf("%s\x00%d\x00%d\x00%s", c.FilePath, start, end, c.Body)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
	}
	return out
}
