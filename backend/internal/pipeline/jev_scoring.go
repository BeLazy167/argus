package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/jev"
	"github.com/BeLazy167/argus/backend/internal/util"
)

const (
	// jevScoringFPFloor is the false-positive probability required to drop a
	// finding without asking the judge — same bar as the addressed judge's
	// resolve threshold.
	jevScoringFPFloor = 0.97
	// jevScoringDefectCeil is the concrete-defect ceiling that must hold
	// alongside the FP floor: a finding that MIGHT name a real defect goes to
	// the judge even when Jev thinks it's a false positive.
	jevScoringDefectCeil = 0.5
	// jevScoringDropScore is the synthetic group score given to dropped
	// findings — below every severity threshold and minor-note band, so the
	// existing threshold filter removes them while keeping the audit trail.
	jevScoringDropScore = 10
	// jevScoringMaxFindings bounds the batched question count and state size.
	// Larger reviews skip the pre-filter entirely (findings go to the judge).
	jevScoringMaxFindings = 60
)

// jevScoringPreFilter asks Jev to flag findings that are confident false
// positives. The judge never sees them; they re-enter scoring as low-scored
// synthetic groups so AllFileReviews/review_comments keep the audit trail.
// Safe direction is keep: a missing, malformed, or middling answer leaves the
// finding in the judge prompt, and any Jev error drops nothing.
func (ss *ScoringStage) jevScoringPreFilter(ctx context.Context, run *PipelineRun, allComments []indexedComment) (map[int]string, StageTokens) {
	if len(allComments) > jevScoringMaxFindings {
		return nil, StageTokens{}
	}
	j := resolveJevEvaluator(ctx, jevKeyReaderFor(ss.store), ss.jev, run.DBInstallationID, &run.DBRepoID, run.FeatureFlags)
	if j == nil {
		return nil, StageTokens{}
	}
	jevCtx, cancel := context.WithTimeout(ctx, jevEvalTimeout)
	res, err := j.Evaluate(jevCtx, jevScoringState(run, allComments), jevScoringQuestions(len(allComments)), "scoring_fp_filter")
	cancel()
	spend := jevStageTokens(res)
	if err != nil {
		slog.WarnContext(ctx, "jev scoring pre-filter failed, all findings go to the judge",
			"error", err, "pr", run.PREvent.PRNumber)
		return nil, spend
	}
	dropped := make(map[int]string)
	for i := range allComments {
		fp := res.Noul(fmt.Sprintf("fp_%d", i))
		defect := res.Noul(fmt.Sprintf("defect_%d", i))
		if fp == nil || defect == nil {
			continue
		}
		if *fp >= jevScoringFPFloor && *defect <= jevScoringDefectCeil {
			dropped[i] = fmt.Sprintf("jev pre-filter: false-positive p=%.2f, concrete-defect p=%.2f", *fp, *defect)
		}
	}
	if len(dropped) > 0 {
		slog.InfoContext(ctx, "jev scoring pre-filter excluded findings from the judge prompt",
			"dropped", len(dropped), "total", len(allComments), "pr", run.PREvent.PRNumber)
	}
	return dropped, spend
}

// jevScoringState gives Jev the same per-finding lines the judge prompt uses,
// plus the PR header, so the two can't drift.
func jevScoringState(run *PipelineRun, allComments []indexedComment) map[string]any {
	findings := make([]string, 0, len(allComments))
	for i, ic := range allComments {
		c := run.FileReviews[ic.fileIdx].Comments[ic.commentIdx]
		findings = append(findings, scoringFindingText(i, run.FileReviews[ic.fileIdx].Path, c))
	}
	var pr strings.Builder
	safeTitle := sanitizeUserInput(util.Truncate(run.PREvent.PRTitle, 200, false))
	safeAuthor := sanitizeUserInput(util.Truncate(run.PREvent.PRAuthor, 100, false))
	pr.WriteString(fmt.Sprintf("PR #%d: %q by %s", run.PREvent.PRNumber, safeTitle, safeAuthor))
	if run.PREvent.PRBody != "" {
		pr.WriteString("\n" + wrapSafeDelimiters("pr_description", sanitizeUserInput(util.Truncate(run.PREvent.PRBody, 1500, false))))
	}
	if run.Contract != nil {
		pr.WriteString("\n" + wrapSafeDelimiters("review_contract", run.Contract.SummaryLine()))
	}
	return map[string]any{"pr": pr.String(), "findings": findings}
}

// jevScoringQuestions emits two nouls per finding: the drop condition needs a
// confident "false positive" AND an unconfident "concrete defect" so a single
// uncertain axis keeps the finding for the judge.
func jevScoringQuestions(n int) map[string]jev.Question {
	qs := make(map[string]jev.Question, 2*n)
	for i := 0; i < n; i++ {
		qs[fmt.Sprintf("fp_%d", i)] = jev.NoulQuestion(
			fmt.Sprintf("Is `findings` entry %d a false positive or not worth an inline review comment?", i),
			"The finding is speculation without concrete evidence, style-only, linter-catchable, or misreads the code",
			"The finding may describe a real problem worth showing the author",
		)
		qs[fmt.Sprintf("defect_%d", i)] = jev.NoulQuestion(
			fmt.Sprintf("Does `findings` entry %d identify a concrete defect with specific evidence?", i),
			"The finding names a plausible correctness, security, or regression risk with evidence",
			"The finding is speculative, stylistic, or lacks evidence of a real defect",
		)
	}
	return qs
}

// survivingFileReviews returns the FileReviews view minus dropped comments
// (prompt indexing stays contiguous per buildScoringPrompt's enumeration) and
// the surviving flat indices, so survivors[p] maps prompt index p back to the
// original flat index for group translation.
func survivingFileReviews(run *PipelineRun, allComments []indexedComment, dropped map[int]string) ([]FileReview, []int) {
	reviews := make([]FileReview, len(run.FileReviews))
	for i, fr := range run.FileReviews {
		reviews[i] = FileReview{Path: fr.Path}
	}
	survivors := make([]int, 0, len(allComments)-len(dropped))
	for i, ic := range allComments {
		if _, ok := dropped[i]; ok {
			continue
		}
		survivors = append(survivors, i)
		fr := &reviews[ic.fileIdx]
		fr.Comments = append(fr.Comments, run.FileReviews[ic.fileIdx].Comments[ic.commentIdx])
	}
	return reviews, survivors
}
