package app

import (
	"context"
	"log/slog"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/graph"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// How the full-index backfill is paced. Tickers are only wake-ups; Postgres
// authoritatively admits at most one fleet window per minimum spacing.
//
// A first window costs one ref lookup, one commit lookup, one recursive tree
// lookup, and one raw-blob lookup per file: 3 + 95 = 98 calls. Continuations
// reuse the immutable commit but repeat the two tree lookups, so charging every
// window the first-window bound stays conservative.
//
// Reservations are spaced 12 minutes apart, but GitHub quotas actual call time.
// Work reserved just before a quota reset can slip into the next quota hour, so
// the bound includes one straddling window: floor(60/12) + 1 = 6 windows. Thus
// 6 * 98 = 588 calls/hour, leaving 5,000 - 588 = 4,412 minimum-quota calls for
// reviews, above their 4,400-call reserve. A 1,371-source-file repo needs 15
// windows and 168 minutes of spacing from first admission to last without fleet
// contention.
const (
	graphIndexInterval             = time.Hour
	graphIndexContinuationInterval = 2 * time.Minute
	graphIndexMinimumSpacing       = 12 * time.Minute
	graphIndexBudgetHour           = time.Hour
	graphIndexStaleness            = 14 * 24 * time.Hour
	graphIndexPerTick              = 1
	graphIndexTimeout              = 25 * time.Minute
	graphIndexFileCap              = graph.DefaultFullIndexFileCap

	minimumGitHubInstallationQuota       = 5000
	graphIndexFixedCallsPerWindow        = 3
	graphIndexCallsPerFile               = 1
	graphIndexMaxCallsPerWindow          = graphIndexFixedCallsPerWindow + graphIndexCallsPerFile*graphIndexFileCap
	graphIndexMaxCallsPerHour            = 600
	graphIndexReservedReviewCallsPerHour = 4400
)

// runGraphIndexBackfill walks whole repositories into the code graph on a
// schedule.
//
// This is the authoritative recall path for the code graph. Before this
// backfill, only pull-request-shaped fragments were indexed, so traversal over
// the projection returned a fragment that looked like a real answer.
//
// Runs until ctx is cancelled. Every failure is logged and skipped rather than
// returned: this is a background improvement to review quality, and it must
// never take the server down or stop trying because one repository is
// unreachable.
func runGraphIndexBackfill(ctx context.Context, db *store.Store, ghClient *ghpkg.Client, logger *slog.Logger) {
	workerStarted := time.Now()
	logger.InfoContext(ctx, "graph index backfill worker started",
		"initial_delay", 2*time.Minute, "prompt_interval", graphIndexContinuationInterval,
		"backfill_interval", graphIndexInterval, "minimum_window_spacing", graphIndexMinimumSpacing,
		"staleness", graphIndexStaleness, "repos_per_tick", graphIndexPerTick,
		"repo_timeout", graphIndexTimeout, "file_cap", graphIndexFileCap)
	defer func() {
		logger.InfoContext(context.WithoutCancel(ctx), "graph index backfill scheduler stopped",
			"duration_ms", time.Since(workerStarted).Milliseconds(), "reason", ctx.Err())
	}()
	// First backfill soon after boot, not a full interval later. Prompt refreshes
	// then use their own short poll; ordinary stale repositories keep the hourly
	// pacing that protects the installation's GitHub budget.
	first := time.NewTimer(2 * time.Minute)
	defer first.Stop()
	select {
	case <-ctx.Done():
		logger.InfoContext(context.WithoutCancel(ctx), "graph index initial delay cancelled", "reason", ctx.Err())
		return
	case <-first.C:
		logger.InfoContext(ctx, "graph index initial tick due", "tick_kind", "stale")
		indexDueRepos(ctx, db, ghClient, logger, false)
	}

	promptTicker := time.NewTicker(graphIndexContinuationInterval)
	defer promptTicker.Stop()
	backfillTicker := time.NewTicker(graphIndexInterval)
	defer backfillTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-promptTicker.C:
			logger.InfoContext(ctx, "graph index tick due", "tick_kind", "prompt")
			indexDueRepos(ctx, db, ghClient, logger, true)
		case <-backfillTicker.C:
			logger.InfoContext(ctx, "graph index tick due", "tick_kind", "stale")
			indexDueRepos(ctx, db, ghClient, logger, false)
		}
	}
}

// indexDueRepos performs one tick's worth of indexing.
//
// Split from the loop so the scheduling and the work can be reasoned about —
// and tested — separately.
func indexDueRepos(ctx context.Context, db *store.Store, ghClient *ghpkg.Client, logger *slog.Logger, promptOnly bool) {
	tickKind := "stale"
	if promptOnly {
		tickKind = "prompt"
	}
	tickStarted := time.Now()
	logger.InfoContext(ctx, "graph index cycle started", "tick_kind", tickKind, "limit", graphIndexPerTick)
	completed := false
	defer func() {
		if !completed {
			logger.InfoContext(context.WithoutCancel(ctx), "graph index cycle stopped",
				"tick_kind", tickKind, "duration_ms", time.Since(tickStarted).Milliseconds(), "reason", ctx.Err())
		}
	}()
	complete := func(outcome string, attrs ...any) {
		args := []any{"tick_kind", tickKind, "outcome", outcome, "duration_ms", time.Since(tickStarted).Milliseconds()}
		args = append(args, attrs...)
		logger.InfoContext(context.WithoutCancel(ctx), "graph index cycle completed", args...)
		completed = true
	}

	// Only one machine indexes at a time. Both run this loop, and a duplicated
	// full index costs a second 1500-call burst against the same installation's
	// GitHub budget — the budget in-flight reviews are drawing on.
	lockStarted := time.Now()
	logger.InfoContext(ctx, "graph index fleet lock acquisition started", "tick_kind", tickKind)
	acquired, release, err := db.TryGraphIndexLock(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "graph index fleet lock acquisition failed", "tick_kind", tickKind,
			"duration_ms", time.Since(lockStarted).Milliseconds(), "error", err)
		complete("lock_error")
		return
	}
	logger.InfoContext(ctx, "graph index fleet lock acquisition completed", "tick_kind", tickKind,
		"acquired", acquired, "duration_ms", time.Since(lockStarted).Milliseconds())
	if !acquired {
		complete("lock_not_acquired")
		return
	}
	defer func() {
		release()
		logger.InfoContext(context.WithoutCancel(ctx), "graph index fleet lock released", "tick_kind", tickKind)
	}()

	listCtx, cancelList := context.WithTimeout(ctx, 30*time.Second)
	listStarted := time.Now()
	logger.InfoContext(listCtx, "graph index due repository query started", "tick_kind", tickKind, "limit", graphIndexPerTick)
	var targets []store.RepoIndexTarget
	if promptOnly {
		targets, err = db.ListReposDueForPromptGraphIndex(listCtx, graphIndexPerTick)
	} else {
		targets, err = db.ListReposDueForGraphIndex(listCtx, graphIndexStaleness, graphIndexPerTick)
	}
	cancelList()
	if err != nil {
		logger.ErrorContext(ctx, "graph index due repository query failed", "tick_kind", tickKind,
			"duration_ms", time.Since(listStarted).Milliseconds(), "error", err)
		complete("list_error")
		return
	}
	logger.InfoContext(ctx, "graph index due repository query completed", "tick_kind", tickKind,
		"target_count", len(targets), "duration_ms", time.Since(listStarted).Milliseconds())
	if len(targets) == 0 {
		complete("no_due_repositories", "target_count", 0)
		return
	}

	reserveStarted := time.Now()
	logger.InfoContext(ctx, "graph index API window reservation started", "tick_kind", tickKind,
		"minimum_spacing", graphIndexMinimumSpacing)
	reserved, err := db.TryReserveGraphIndexWindow(ctx, graphIndexMinimumSpacing)
	if err != nil {
		logger.ErrorContext(ctx, "graph index API window reservation failed", "tick_kind", tickKind,
			"duration_ms", time.Since(reserveStarted).Milliseconds(), "error", err)
		complete("reservation_error", "target_count", len(targets))
		return
	}
	logger.InfoContext(ctx, "graph index API window reservation completed", "tick_kind", tickKind,
		"reserved", reserved, "duration_ms", time.Since(reserveStarted).Milliseconds())
	if !reserved {
		complete("window_not_reserved", "target_count", len(targets))
		return
	}

	rebuilt := false
	succeeded := 0
	failed := 0
	staged := 0
	unchanged := 0
	for _, t := range targets {
		repo := t.Owner + "/" + t.Repo
		repoStarted := time.Now()
		logger.InfoContext(ctx, "graph index repository attempt started", "tick_kind", tickKind,
			"repo", repo, "repo_id", t.RepoID, "installation_id", t.GitHubInstallationID,
			"default_branch", t.DefaultBranch, "cursor", t.IndexCursor, "timeout", graphIndexTimeout)
		// Stamp the attempt BEFORE trying. A repo that reliably kills the worker
		// — OOM, deadline, panic — would otherwise never record anything and
		// would be re-selected forever, starving every repo behind it.
		if err := db.MarkRepoGraphAttempted(ctx, t.RepoID); err != nil {
			logger.ErrorContext(ctx, "graph index repository attempt stamp failed", "tick_kind", tickKind,
				"repo", repo, "repo_id", t.RepoID, "error", err)
		} else {
			logger.InfoContext(ctx, "graph index repository attempt stamped", "tick_kind", tickKind,
				"repo", repo, "repo_id", t.RepoID)
		}

		// Per-repo deadline. A single unreachable repository must not hold the
		// backfill open until the next tick arrives and overlaps it.
		repoCtx, cancel := context.WithTimeout(ctx, graphIndexTimeout)
		result, err := graph.IndexRepoBounded(repoCtx, db, ghClient,
			t.GitHubInstallationID, t.Owner, t.Repo, t.DefaultBranch, t.RepoID,
			graphIndexFileCap, t.IndexCursor)
		cancel()

		if err != nil {
			failed++
			// Deliberately does NOT mark the repo indexed, so it stays at the
			// front of the queue and is retried next tick instead of waiting out
			// a full staleness window.
			logger.ErrorContext(ctx, "graph index repository attempt failed", "tick_kind", tickKind,
				"repo", repo, "repo_id", t.RepoID, "duration_ms", time.Since(repoStarted).Milliseconds(), "error", err)
			continue
		}

		if result.Unchanged {
			unchanged++
			succeeded++
			logger.InfoContext(ctx, "graph index repository attempt completed", "tick_kind", tickKind,
				"repo", repo, "repo_id", t.RepoID, "outcome", "unchanged",
				"commit", result.Snapshot.PublishedCommitSHA, "duration_ms", time.Since(repoStarted).Milliseconds())
			continue
		}
		if !result.Published {
			staged++
			succeeded++
			logger.WarnContext(ctx, "graph index repository attempt completed", "tick_kind", tickKind,
				"repo", repo, "repo_id", t.RepoID, "outcome", "staged_incomplete", "commit", result.Snapshot.CommitSHA,
				"staged", result.Staged, "remaining", result.Remaining,
				"visited", result.Snapshot.VisitedFiles, "expected", result.Snapshot.ExpectedFiles,
				"failed", result.Snapshot.FailedFiles, "unavailable", result.Snapshot.UnavailableFiles,
				"duration_ms", time.Since(repoStarted).Milliseconds())
			continue
		}
		succeeded++
		logger.InfoContext(ctx, "graph index repository generation published", "tick_kind", tickKind,
			"repo", repo, "repo_id", t.RepoID, "commit", result.Snapshot.CommitSHA,
			"files", result.Snapshot.VisitedFiles, "skipped", result.Snapshot.SkippedFiles,
			"unavailable", result.Snapshot.UnavailableFiles, "duration_ms", time.Since(repoStarted).Milliseconds())
		rebuilt = true
	}

	if !rebuilt {
		complete("repositories_processed", "target_count", len(targets), "succeeded", succeeded,
			"failed", failed, "staged", staged, "unchanged", unchanged, "projection_rebuilt", false)
		return
	}
	// One rebuild per tick, after the repos, not once per repo. graph.build()
	// re-reads the whole projection, so doing it per repo would repeat the same
	// full scan for no extra freshness.
	buildCtx, cancelBuild := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelBuild()
	projectionStarted := time.Now()
	logger.InfoContext(buildCtx, "graph index projection rebuild started", "tick_kind", tickKind, "timeout", 5*time.Minute)
	if err := db.RebuildCodeGraphProjection(buildCtx); err != nil {
		// Non-fatal: blast radius falls back to the recursive CTE, which is
		// correct, just slower. The rows are written either way.
		logger.WarnContext(buildCtx, "graph index projection rebuild failed", "tick_kind", tickKind,
			"duration_ms", time.Since(projectionStarted).Milliseconds(), "error", err)
		complete("repositories_processed", "target_count", len(targets), "succeeded", succeeded,
			"failed", failed, "staged", staged, "unchanged", unchanged, "projection_rebuilt", false)
		return
	}
	logger.InfoContext(buildCtx, "graph index projection rebuild completed", "tick_kind", tickKind,
		"duration_ms", time.Since(projectionStarted).Milliseconds())
	complete("repositories_processed", "target_count", len(targets), "succeeded", succeeded,
		"failed", failed, "staged", staged, "unchanged", unchanged, "projection_rebuilt", true)
}
