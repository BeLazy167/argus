// Package pipeline: cost_estimator.go builds a TriggerEstimate for the
// "Trigger review" checkbox comment by combining repo-level historical review
// stats (tokens + cost) with a best-effort live GitHub diff-size lookup.
//
// The estimator is intentionally cheap: one DB query and one GitHub API call.
// Both sides are independent — a failure on either still yields a usable
// estimate (e.g., historical-only when ListFiles fails; live-only when the
// repo has no prior reviews).
package pipeline

import (
	"context"
	"log/slog"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// historicalReviewSampleLimit caps how many past reviews feed the moving avg.
// 20 is small enough to reflect recent PR sizes but large enough to absorb a
// single outlier (e.g., one 500-file refactor) without distorting the mean.
const historicalReviewSampleLimit = 20

// listFilesTimeout bounds the live GitHub call. Users are waiting on a comment
// post that must happen synchronously with the webhook handler; 6s is the
// sweet spot between "most PRs finish" and "don't block the webhook reply".
const listFilesTimeout = 6 * time.Second

// statsQueryTimeout bounds the historical-reviews aggregate query. 2s matches
// the project's other memory/DB timeouts (see CLAUDE.md memory) and keeps the
// webhook goroutine from stalling on an unhealthy DB.
const statsQueryTimeout = 2 * time.Second

// BuildEstimate composes a TriggerEstimate from two independent sources:
//
//   - Historical: Store.GetRepoReviewStats averages tokens + cost over the
//     last N completed reviews for repoID. Falls through on error — the
//     resulting estimate simply omits the historical line.
//   - Live: ghClient.GetPRFiles walks the PR's changed files to sum additions
//     + deletions and refine the file count. Failure is logged at Warn and
//     DiffLines stays 0; the caller's rendered comment skips that line.
//
// fileCountHint comes from the webhook PREvent payload so the estimate still
// shows a file count even if ListFiles fails. The live file count, when
// available, takes precedence because the webhook's value can lag by the time
// the handler runs.
func BuildEstimate(
	ctx context.Context,
	st *store.Store,
	ghClient *ghpkg.Client,
	installationID, repoID int64,
	owner, repo string,
	prNumber, fileCountHint int,
	logger *slog.Logger,
) TriggerEstimate {
	est := TriggerEstimate{Files: fileCountHint}

	statsCtx, cancelStats := context.WithTimeout(ctx, statsQueryTimeout)
	stats, err := st.GetRepoReviewStats(statsCtx, repoID, historicalReviewSampleLimit)
	cancelStats()
	if err != nil {
		logger.Warn("repo review stats query failed", "error", err, "repo_id", repoID)
	} else {
		est.AvgTokens = stats.AvgTokens
		est.SampleSize = stats.SampleSize
		if stats.CostAvailable {
			cost := stats.AvgCost
			est.AvgCostUSD = &cost
		}
	}

	filesCtx, cancelFiles := context.WithTimeout(ctx, listFilesTimeout)
	files, err := ghClient.GetPRFiles(filesCtx, installationID, owner, repo, prNumber)
	cancelFiles()
	if err != nil {
		logger.Warn("PR files lookup failed for estimate", "error", err, "pr", prNumber, "repo", owner+"/"+repo)
		return est
	}
	// Live file count is authoritative — force-pushes can shrink a PR, so we
	// overwrite the hint unconditionally once ListFiles succeeds. The hint only
	// matters in the error path above.
	est.Files = len(files)
	for _, f := range files {
		est.DiffLines += f.GetAdditions() + f.GetDeletions()
	}
	return est
}
