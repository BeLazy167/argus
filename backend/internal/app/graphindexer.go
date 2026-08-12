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
	// First backfill soon after boot, not a full interval later. Prompt refreshes
	// then use their own short poll; ordinary stale repositories keep the hourly
	// pacing that protects the installation's GitHub budget.
	first := time.NewTimer(2 * time.Minute)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
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
			indexDueRepos(ctx, db, ghClient, logger, true)
		case <-backfillTicker.C:
			indexDueRepos(ctx, db, ghClient, logger, false)
		}
	}
}

// indexDueRepos performs one tick's worth of indexing.
//
// Split from the loop so the scheduling and the work can be reasoned about —
// and tested — separately.
func indexDueRepos(ctx context.Context, db *store.Store, ghClient *ghpkg.Client, logger *slog.Logger, promptOnly bool) {
	// Only one machine indexes at a time. Both run this loop, and a duplicated
	// full index costs a second 1500-call burst against the same installation's
	// GitHub budget — the budget in-flight reviews are drawing on.
	acquired, release, err := db.TryGraphIndexLock(ctx)
	if err != nil {
		logger.Error("graph index: taking lock", "error", err)
		return
	}
	if !acquired {
		return
	}
	defer release()

	listCtx, cancelList := context.WithTimeout(ctx, 30*time.Second)
	var targets []store.RepoIndexTarget
	if promptOnly {
		targets, err = db.ListReposDueForPromptGraphIndex(listCtx, graphIndexPerTick)
	} else {
		targets, err = db.ListReposDueForGraphIndex(listCtx, graphIndexStaleness, graphIndexPerTick)
	}
	cancelList()
	if err != nil {
		logger.Error("graph index: listing due repos", "error", err)
		return
	}
	if len(targets) == 0 {
		return
	}
	reserved, err := db.TryReserveGraphIndexWindow(ctx, graphIndexMinimumSpacing)
	if err != nil {
		logger.Error("graph index: reserving persistent API budget", "error", err)
		return
	}
	if !reserved {
		return
	}

	rebuilt := false
	for _, t := range targets {
		// Stamp the attempt BEFORE trying. A repo that reliably kills the worker
		// — OOM, deadline, panic — would otherwise never record anything and
		// would be re-selected forever, starving every repo behind it.
		if err := db.MarkRepoGraphAttempted(ctx, t.RepoID); err != nil {
			logger.Error("graph index: marking attempt", "repo", t.Owner+"/"+t.Repo, "error", err)
		}

		// Per-repo deadline. A single unreachable repository must not hold the
		// backfill open until the next tick arrives and overlaps it.
		repoCtx, cancel := context.WithTimeout(ctx, graphIndexTimeout)
		result, err := graph.IndexRepoBounded(repoCtx, db, ghClient,
			t.GitHubInstallationID, t.Owner, t.Repo, t.DefaultBranch, t.RepoID,
			graphIndexFileCap, t.IndexCursor)
		cancel()

		if err != nil {
			// Deliberately does NOT mark the repo indexed, so it stays at the
			// front of the queue and is retried next tick instead of waiting out
			// a full staleness window.
			logger.Error("graph index: full index failed",
				"repo", t.Owner+"/"+t.Repo, "error", err)
			continue
		}

		if result.Unchanged {
			logger.Info("graph index: published generation already matches default branch",
				"repo", t.Owner+"/"+t.Repo, "commit", result.Snapshot.PublishedCommitSHA)
			continue
		}
		if !result.Published {
			logger.Warn("graph index: generation window staged but not complete",
				"repo", t.Owner+"/"+t.Repo, "commit", result.Snapshot.CommitSHA,
				"staged", result.Staged, "remaining", result.Remaining,
				"visited", result.Snapshot.VisitedFiles, "expected", result.Snapshot.ExpectedFiles,
				"failed", result.Snapshot.FailedFiles, "unavailable", result.Snapshot.UnavailableFiles)
			continue
		}
		logger.Info("graph index: full generation published",
			"repo", t.Owner+"/"+t.Repo, "commit", result.Snapshot.CommitSHA,
			"files", result.Snapshot.VisitedFiles, "skipped", result.Snapshot.SkippedFiles,
			"unavailable", result.Snapshot.UnavailableFiles)
		rebuilt = true
	}

	if !rebuilt {
		return
	}
	// One rebuild per tick, after the repos, not once per repo. graph.build()
	// re-reads the whole projection, so doing it per repo would repeat the same
	// full scan for no extra freshness.
	buildCtx, cancelBuild := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelBuild()
	if err := db.RebuildCodeGraphProjection(buildCtx); err != nil {
		// Non-fatal: blast radius falls back to the recursive CTE, which is
		// correct, just slower. The rows are written either way.
		logger.Warn("graph index: pgGraph projection rebuild failed", "error", err)
	}
}
