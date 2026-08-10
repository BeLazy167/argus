// Package pipeline: budget.go applies the review Budget.
//
// The verdict is decided in internal/admission, which knows nothing about
// pipelines or databases. This file supplies the facts and carries out the
// answer.
package pipeline

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/BeLazy167/argus/backend/internal/admission"
	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/pkg/diff"
)

// budgetStatsTimeout bounds the history lookup. The Budget must not be the
// reason a review is slow to start, and a missing estimate is survivable —
// size still decides.
const budgetStatsTimeout = 2 * time.Second

// checkBudget returns the verdict for what this run is about to cost.
//
// Size comes from the fetched diff, so it is exact and free. The token estimate
// comes from this repo's own recent reviews and abstains when there are none —
// a brand-new repo has no history, and a measure that abstains must not be the
// only measure, which is why size is checked too.
func (o *Orchestrator) checkBudget(ctx context.Context, run *PipelineRun, dbRepo *store.Repo) admission.Verdict {
	if run == nil || run.Diff == nil {
		return admission.Allow()
	}

	size := admission.Size{
		Files: len(run.Diff.Files),
		Lines: run.Diff.TotalLinesChanged(),
	}

	var est admission.TokenEstimate
	statsCtx, cancel := context.WithTimeout(ctx, budgetStatsTimeout)
	stats, err := o.st.GetRepoReviewStats(statsCtx, dbRepo.ID, historicalReviewSampleLimit)
	cancel()
	if err != nil {
		// Not fatal, and deliberately not a refusal: the history lookup failing
		// says nothing about this pull request. Size still decides.
		o.logger.Warn("budget: repo review stats", "error", err, "repo_id", dbRepo.ID)
	} else {
		est = admission.TokenEstimate{AvgTokens: stats.AvgTokens, Samples: stats.SampleSize}
	}

	// The Budget arm only. Permission and the rate limit were decided at the
	// launch site, before the diff existed; this is the one gate that needs it.
	//
	// Calling Decide here with nil dependencies would dress that up as crossing
	// the seam while running exactly this one check, so it says what it means.
	return admission.Budget(size, est, run.BudgetLimits)
}

// applyReduce narrows a run that costs more than its limit allows.
//
// Both multipliers are cut, because either alone leaves a shape of pull request
// unbounded. Dropping depth turns 2000 calls into 600 on a 600-file request,
// which is a real saving and still an enormous review; capping files without
// dropping depth leaves fifty deep files costing 200 calls.
func (o *Orchestrator) applyReduce(ctx context.Context, run *PipelineRun, v admission.Verdict) {
	if v.ForceShallow {
		run.DeepReview = false
	}
	run.BudgetMaxFiles = v.MaxFiles
	run.BudgetNote = v.Reason

	o.logger.Info("review reduced by budget",
		"review_id", run.ReviewID, "repo", run.PREvent.RepoFullName,
		"files", len(run.Diff.Files), "max_files", v.MaxFiles,
		"shallow", v.ForceShallow, "reason", v.Reason)

	// Onto the review row as well as the run: the dashboard reads reviews, and
	// the run's copy lives in pipeline_states where no reader looks. Non-fatal
	// — losing the note costs an explanation, not the reduction.
	//
	// The nil check is for a hand-constructed Orchestrator, which is what the
	// tests of this function use: the reduction itself is pure and worth
	// testing without a database behind it.
	if o.db != nil {
		if _, err := o.db.Exec(context.WithoutCancel(ctx),
			`UPDATE reviews SET budget_note = $2 WHERE id = $1`, run.ReviewID, v.Reason); err != nil {
			o.logger.Warn("budget: persisting note", "error", err, "review_id", run.ReviewID)
		}
	}

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventBudgetReduced, map[string]any{
			"max_files": v.MaxFiles,
			"shallow":   v.ForceShallow,
			"reason":    v.Reason,
		})
	}
}

// refuseForBudget records the refusal and tells the pull request why.
//
// The review row already exists, so the refusal is a terminal status rather
// than a silent return: the dashboard shows it, and retry re-evaluates it
// rather than bypassing it.
func (o *Orchestrator) refuseForBudget(ctx context.Context, run *PipelineRun, event ghpkg.PREvent, v admission.Verdict) {
	o.logger.Info("review refused by budget",
		"review_id", run.ReviewID, "repo", event.RepoFullName, "pr", event.PRNumber,
		"files", len(run.Diff.Files), "lines", run.Diff.TotalLinesChanged(), "reason", v.Reason)

	dbCtx := context.WithoutCancel(ctx)
	if _, err := o.st.UpdateReviewStatusIf(dbCtx, run.ReviewID, "failed", v.Reason, nil,
		[]string{"pending", "in_progress"}); err != nil {
		o.logger.Error("budget refusal: persisting status", "error", err, "review_id", run.ReviewID)
	}

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventError, map[string]string{"error": v.Reason})
	}

	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		return
	}
	body := fmt.Sprintf("> **Argus** did not review this pull request.\n>\n> %s", v.Reason)
	if err := o.ghClient.CreateIssueComment(dbCtx, event.InstallationID, owner, repo, event.PRNumber, body); err != nil {
		o.logger.Warn("budget refusal: posting comment", "error", err, "pr", event.PRNumber)
	}
}

// budgetCapFiles returns the files a run may review, honouring BudgetMaxFiles.
//
// Returns the diff untouched when no cap is set, which is every ordinary review.
//
// When a cap applies, files are ordered by triage risk and the tail is dropped.
// Skipped files never occupy a slot: they cost nothing to review, so letting
// them consume the cap would spend it on work that was not going to happen.
func budgetCapFiles(run *PipelineRun, triageLookup map[string]TriageAction) []diff.FileDiff {
	if run.Diff == nil {
		return nil
	}
	if run.BudgetMaxFiles <= 0 {
		return run.Diff.Files
	}

	// Rank: deep first, then security, then skim. Equal ranks keep diff order,
	// so the selection is deterministic for a given pull request.
	rank := func(a TriageAction) int {
		switch a {
		case TriageDeep:
			return 0
		case TriageSecuritySkim:
			return 1
		default:
			return 2
		}
	}

	kept := make([]diff.FileDiff, 0, len(run.Diff.Files))
	for _, f := range run.Diff.Files {
		if triageLookup[f.NewName] == TriageSkip {
			continue
		}
		kept = append(kept, f)
	}
	// Under the cap there is nothing to choose between, so the diff order is
	// left alone. Sorting unconditionally would reorder every capped review's
	// output for no reason.
	if len(kept) <= run.BudgetMaxFiles {
		return kept
	}

	sort.SliceStable(kept, func(i, j int) bool {
		return rank(triageLookup[kept[i].NewName]) < rank(triageLookup[kept[j].NewName])
	})
	return kept[:run.BudgetMaxFiles]
}
