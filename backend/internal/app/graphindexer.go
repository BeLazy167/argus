package app

import (
	"context"
	"log/slog"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/graph"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// How the full-index backfill is paced.
//
// One repo per tick, hourly. The pacing is set by the two costs a full index
// carries, and both are per-installation:
//
//   - GitHub API. indexFileSet fetches one file per call, so a capped index is
//     up to DefaultFullIndexFileCap calls. An installation gets 5000/hour, and
//     those same calls are what reviews need — so a burst that indexed several
//     repos at once could starve the thing users are waiting on.
//   - Memory. The cross-file edge-resolution pass retains parsed symbols for
//     every file in the run. One repo at a time keeps that to a single repo's
//     worth on a 1024 MB machine.
//
// At one repo an hour a 25-repo installation reaches full coverage in about a
// day, and the refresh interval then keeps it current. Deliberately slower than
// necessary: this is backfill, and the per-PR incremental path already keeps
// changed files fresh in the meantime.
const (
	graphIndexInterval  = time.Hour
	graphIndexStaleness = 14 * 24 * time.Hour
	graphIndexPerTick   = 1
	graphIndexTimeout   = 25 * time.Minute
)

// runGraphIndexBackfill walks whole repositories into the code graph on a
// schedule.
//
// This is the missing recall half of the code graph. graph.IndexFiles runs on
// every pull request but only over that PR's changed files, so the graph grew
// as disconnected PR-shaped islands — 29% of nodes isolated — and traversal
// over it returns a fragment that looks like a real answer. graph.IndexRepo
// existed to fix that and had no caller.
//
// Runs until ctx is cancelled. Every failure is logged and skipped rather than
// returned: this is a background improvement to review quality, and it must
// never take the server down or stop trying because one repository is
// unreachable.
func runGraphIndexBackfill(ctx context.Context, db *store.Store, ghClient *ghpkg.Client, logger *slog.Logger) {
	// First tick soon after boot, not a full interval later. Machines restart on
	// every deploy and can be stopped by autostop, so a loop that only ever fires
	// at T+1h would index nothing on a machine that never stays up an hour.
	// Delayed a little so it does not compete with startup recovery work.
	first := time.NewTimer(2 * time.Minute)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
		indexDueRepos(ctx, db, ghClient, logger)
	}

	ticker := time.NewTicker(graphIndexInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			indexDueRepos(ctx, db, ghClient, logger)
		}
	}
}

// indexDueRepos performs one tick's worth of indexing.
//
// Split from the loop so the scheduling and the work can be reasoned about —
// and tested — separately.
func indexDueRepos(ctx context.Context, db *store.Store, ghClient *ghpkg.Client, logger *slog.Logger) {
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
	targets, err := db.ListReposDueForGraphIndex(listCtx, graphIndexStaleness, graphIndexPerTick)
	cancelList()
	if err != nil {
		logger.Error("graph index: listing due repos", "error", err)
		return
	}
	if len(targets) == 0 {
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
		indexed, dropped, nextCursor, err := graph.IndexRepoBounded(repoCtx, db, ghClient,
			t.GitHubInstallationID, t.Owner, t.Repo, t.DefaultBranch, t.RepoID,
			graph.DefaultFullIndexFileCap, t.IndexCursor)
		cancel()

		if err != nil {
			// Deliberately does NOT mark the repo indexed, so it stays at the
			// front of the queue and is retried next tick instead of waiting out
			// a full staleness window.
			logger.Error("graph index: full index failed",
				"repo", t.Owner+"/"+t.Repo, "error", err)
			continue
		}

		if dropped > 0 {
			// Say so. A capped index recorded as a complete one is the same
			// false confidence the fragmented graph already produces. The cursor
			// means the next run covers a different window, so coverage still
			// completes — over several runs rather than one.
			logger.Warn("graph index: repo exceeded the file cap, indexed one window",
				"repo", t.Owner+"/"+t.Repo, "indexed", indexed, "skipped", dropped,
				"next_offset", nextCursor)
		}
		markCtx, cancelMark := context.WithTimeout(ctx, 30*time.Second)
		err = db.MarkRepoGraphIndexed(markCtx, t.RepoID, nextCursor)
		cancelMark()
		if err != nil {
			// The index itself succeeded, so do not claim otherwise; the repo is
			// simply re-selected next tick and re-indexed, which is wasteful but
			// correct.
			logger.Error("graph index: marking indexed", "repo", t.Owner+"/"+t.Repo, "error", err)
		}
		logger.Info("graph index: full index complete",
			"repo", t.Owner+"/"+t.Repo, "files", indexed, "skipped", dropped)
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
