package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/util"
)

// ScoringStage validates review comments using a separate scoring model.
// If no scoring model resolves (no repo/org config or no provider key), all
// comments pass through and the posted summary carries an explicit notice —
// unfiltered findings are never silent. Scoring memory context resolves from
// run.Indexer, so the stage needs no memory.Registry of its own.
type ScoringStage struct {
	registry *llm.Registry
	store    *store.Store
	// cfgLister, when non-nil, overrides the store-backed model-config lister
	// (test seam). Production resolution builds a per-run storeConfigLister.
	cfgLister llm.ModelConfigLister
}

func NewScoringStage(registry *llm.Registry, st *store.Store) *ScoringStage {
	return &ScoringStage{registry: registry, store: st}
}

// scoringThresholdForSeverity returns the minimum score for a comment to
// survive scoring, varying by severity and conditioned on the ReviewContract
// class (depth follows blast radius):
//   - one_time_script / docs / generated: suggestion +15, warning +10 — only
//     near-certain findings survive on throwaway/low-stakes changes
//   - migration or security floor: critical -5 — MORE sensitive where the
//     blast radius is data loss or auth
func scoringThresholdForSeverity(severity string, c *ReviewContract) int {
	sev := strings.ToLower(severity)
	var t int
	switch sev {
	case "critical":
		t = 35
	case "warning":
		t = 45
	case "suggestion", "info":
		t = 55
	default:
		t = 45
	}
	if c.Is(ChangeClassOneTimeScript) || c.Is(ChangeClassDocs) || c.Is(ChangeClassGenerated) {
		switch sev {
		case "suggestion", "info":
			t += 15
		case "warning":
			t += 10
		}
	}
	if (c.Is(ChangeClassMigration) || c.HasSecurityFloor()) && sev == "critical" {
		t -= 5
	}
	return t
}

// minorNoteBand is the near-miss window below the severity threshold: findings
// scoring within it are demoted to the summary's collapsed "Minor notes"
// section instead of being dropped outright (caps, not floors — nothing is
// resurrected inline).
const minorNoteBand = 10

// thresholdDisposition classifies where a scored finding lands.
type thresholdDisposition int

const (
	dispositionInline thresholdDisposition = iota
	dispositionMinorNote
	dispositionDrop
)

// thresholdDispositionFor returns the disposition for a score against its
// class-conditioned severity threshold.
func thresholdDispositionFor(score int, severity Severity, c *ReviewContract) thresholdDisposition {
	th := scoringThresholdForSeverity(string(severity), c)
	switch {
	case score >= th:
		return dispositionInline
	case score >= th-minorNoteBand:
		return dispositionMinorNote
	default:
		return dispositionDrop
	}
}

// minorNoteFrom projects a demoted comment into its summary-rendered form.
func minorNoteFrom(path string, c FileComment) MinorNote {
	title := c.What
	if title == "" {
		title = util.Truncate(c.Body, 120, true)
	}
	return MinorNote{Path: path, Line: c.Line, Severity: c.Severity, Title: title}
}

// Execute runs the judge + deterministic caps + threshold filter for EVERY
// review (a single cheap LLM call). Pass2/validate/multi-pass remain gated
// behind deep review elsewhere — only the earned-findings gate is always on.
func (ss *ScoringStage) Execute(ctx context.Context, run *PipelineRun) error {
	// Resolve the judge model: repo row → org row, else scoring is skipped.
	// Configuration-gap skips are never silent — ScoringUnconfigured surfaces
	// a setup notice in the posted summary (scoringSkippedNotice).
	lister := ss.cfgLister
	if lister == nil {
		lister = storeConfigLister{st: ss.store, installationID: run.DBInstallationID}
	}
	provider, cfg, err := ss.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageScoring)
	if err != nil {
		slog.Info("scoring provider unavailable, passing all comments through", "repo_id", run.DBRepoID, "error", err)
		run.ScoringSkipped = true
		// Setup notice ONLY for configuration gaps (sentinel match). A
		// transient lister/key-resolver failure on a fully-configured org
		// must never post a public "configure your models" nudge.
		if errors.Is(err, llm.ErrNoModelConfig) || errors.Is(err, llm.ErrNoAPIKey) {
			run.ScoringUnconfigured = true
			run.ScoringMissingKey = errors.Is(err, llm.ErrNoAPIKey)
		}
		return nil
	}

	// Flatten all comments into indexed list
	var allComments []indexedComment
	for fi, fr := range run.FileReviews {
		for ci := range fr.Comments {
			allComments = append(allComments, indexedComment{fileIdx: fi, commentIdx: ci})
		}
	}
	if len(allComments) == 0 {
		return nil
	}

	// Fetch repo memory context for scoring calibration
	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		slog.Warn("scoring: invalid repo name, skipping memory context", "error", err)
	}
	memContext := fetchScoringContext(ctx, run.Indexer, owner, repo, run.FileReviews)
	memContext += buildPatternTrustCalibration(ctx, ss.store, run.DBInstallationID)

	prompt := buildScoringPrompt(run, memContext)
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      customOrDefault(run.Prompts, "scoring_system", scoringSystemPrompt),
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   cfg.MaxTokens,
		Temperature: cfg.Temperature,
		Stage:       "scoring",
	})
	if err != nil {
		slog.Error("scoring LLM call failed, keeping all comments", "error", err)
		run.ScoringSkipped = true
		return nil // non-fatal
	}

	// Track scoring tokens
	run.Tokens.Scoring = StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}
	run.Tokens.addToTotal(run.Tokens.Scoring)
	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventTokenUpdate, map[string]any{
			"total_tokens": run.Tokens.Total.TotalTokens,
			"cost":         run.Tokens.Total.Cost,
		})
	}

	// Parse judge output (Layer 4: LLM sweeper)
	groups, err := unmarshalLLMArray[judgeGroup](resp.Content)
	if err != nil {
		slog.Error("failed to parse judge response, keeping deterministic results",
			"error", err,
			"model", cfg.Model,
			"provider", cfg.Provider,
			"finish_reason", resp.FinishReason,
			"response_len", len(resp.Content),
			"response_prefix", util.Truncate(resp.Content, 300, true))
		run.ScoringSkipped = true
		return nil // non-fatal — deterministic dedup results stand
	}

	// Validate structural integrity
	if vErr := validateJudgeOutput(groups, len(allComments)); vErr != nil {
		slog.Warn("judge output invalid, keeping deterministic results",
			"error", vErr,
			"groups", len(groups),
			"total_findings", len(allComments))
		run.ScoringSkipped = true
		return nil // non-fatal — deterministic dedup results stand
	}

	// Clamp scores to [0, 100] — LLMs don't reliably respect numeric ranges
	for i := range groups {
		if groups[i].Score < 0 {
			groups[i].Score = 0
		}
		if groups[i].Score > 100 {
			groups[i].Score = 100
		}
	}

	// Deterministic FP caps / TP floors — post-LLM, pre-threshold
	adjustScores(run, groups, allComments)

	// Apply scores and mark duplicates
	dupSet := make(map[int]bool)
	for _, g := range groups {
		for _, d := range g.Duplicates {
			dupSet[d] = true
		}
		// Apply score to representative
		ic := allComments[g.Representative]
		rep := &run.FileReviews[ic.fileIdx].Comments[ic.commentIdx]
		rep.Score = g.Score
		// Override severity if the judge changed it
		if sev := Severity(g.Severity); ValidSeverities[sev] {
			rep.Severity = sev
		}
		// Track how many were merged by judge
		if len(g.Duplicates) > 0 {
			existing := rep.DedupCount
			if existing == 0 {
				existing = 1
			}
			rep.DedupCount = existing + len(g.Duplicates)
		}

		// Severity floor + body merge — see promoteGroupSeverity.
		if promotion := promoteGroupSeverity(run, g, allComments); promotion.promoted {
			slog.Info("[scoring] promoted rep severity to group max",
				"rep", g.Representative, "from", promotion.fromSev, "to", promotion.toSev,
				"pr", run.PREvent.PRNumber)
		}
	}
	// Build representative set for O(1) lookup
	repSet := make(map[int]bool, len(groups))
	for _, g := range groups {
		repSet[g.Representative] = true
	}
	// Judge-omitted findings default to their severity threshold (borderline):
	// they survive inline but earn no headroom. The old default of 100 let the
	// judge accidentally bless findings by ignoring them.
	var defaultScored int
	for i, ic := range allComments {
		if dupSet[i] || repSet[i] {
			continue
		}
		c := &run.FileReviews[ic.fileIdx].Comments[ic.commentIdx]
		c.Score = scoringThresholdForSeverity(string(c.Severity), run.Contract)
		defaultScored++
	}
	if defaultScored > 0 {
		slog.Warn("scoring: findings not mentioned by judge scored at severity threshold",
			"count", defaultScored, "total", len(allComments))
	}

	// Assign confidence levels based on scores
	for fi := range run.FileReviews {
		for ci := range run.FileReviews[fi].Comments {
			run.FileReviews[fi].Comments[ci].Confidence = assignConfidence(run.FileReviews[fi].Comments[ci].Score)
		}
	}

	// Snapshot all scored comments before filtering — pattern learning uses this
	run.AllFileReviews = make([]FileReview, len(run.FileReviews))
	for i, fr := range run.FileReviews {
		comments := make([]FileComment, len(fr.Comments))
		copy(comments, fr.Comments)
		run.AllFileReviews[i] = FileReview{Path: fr.Path, Comments: comments}
	}

	// Build (fileIdx, commentIdx) set for duplicates to skip
	type commentKey struct{ fi, ci int }
	skipSet := make(map[commentKey]bool)
	for d := range dupSet {
		if d < len(allComments) {
			ic := allComments[d]
			skipSet[commentKey{ic.fileIdx, ic.commentIdx}] = true
		}
	}

	// Filter: remove duplicates; below-threshold comments in the near-miss band
	// become collapsed Minor notes in the summary, the rest are dropped. No
	// survivor floor — findings are earned, never guaranteed (zero inline
	// findings is a valid review).
	var duped int
	kept, minor, dropped := applyThresholdFilter(run, func(fi, ci int) bool {
		if skipSet[commentKey{fi, ci}] {
			duped++
			return true
		}
		return false
	})

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventScoringUpdate, map[string]any{
			"kept":        kept,
			"dropped":     dropped,
			"minor_notes": minor,
			"thresholds": map[string]int{
				"critical":   scoringThresholdForSeverity("critical", run.Contract),
				"warning":    scoringThresholdForSeverity("warning", run.Contract),
				"suggestion": scoringThresholdForSeverity("suggestion", run.Contract),
			},
		})
	}

	slog.Info("scoring complete", "kept", kept, "minor_notes", minor, "dropped", dropped, "duped_by_judge", duped,
		"threshold_critical", scoringThresholdForSeverity("critical", run.Contract),
		"threshold_warning", scoringThresholdForSeverity("warning", run.Contract),
		"threshold_suggestion", scoringThresholdForSeverity("suggestion", run.Contract))
	return nil
}

// scoringSkippedNotice returns the user-visible summary line when the judge
// never ran because scoring is unconfigured (no repo/org config row or no
// provider key — sentinel-matched in Execute). Empty when scoring ran, when
// the skip was transient (judge call/output failure — a setup nudge would
// mislead), or when the review has zero findings (nothing went unfiltered).
func scoringSkippedNotice(run *PipelineRun) string {
	if !run.ScoringSkipped || !run.ScoringUnconfigured {
		return ""
	}
	if countComments(run) == 0 {
		return ""
	}
	if run.ScoringMissingKey {
		return "> ⚠️ Findings were not score-filtered — the scoring model is configured but no API key resolves for its provider. Add one in Settings → API Keys."
	}
	return "> ⚠️ Findings were not score-filtered — set org default models in Settings → Org Defaults."
}

// applyThresholdFilter partitions run.FileReviews by class-conditioned severity
// thresholds: inline survivors stay, near-miss findings (within minorNoteBand
// below threshold) move to run.MinorNotes, the rest are dropped. skip reports
// judge-marked duplicates to exclude entirely. Returns (kept, minor, dropped).
func applyThresholdFilter(run *PipelineRun, skip func(fi, ci int) bool) (kept, minor, dropped int) {
	filtered := run.FileReviews[:0]
	for fi, fr := range run.FileReviews {
		var passing []FileComment
		for ci, c := range fr.Comments {
			if skip != nil && skip(fi, ci) {
				continue
			}
			switch thresholdDispositionFor(c.Score, c.Severity, run.Contract) {
			case dispositionInline:
				passing = append(passing, c)
				kept++
			case dispositionMinorNote:
				run.MinorNotes = append(run.MinorNotes, minorNoteFrom(fr.Path, c))
				minor++
			default:
				dropped++
			}
		}
		if len(passing) > 0 {
			filtered = append(filtered, FileReview{Path: fr.Path, Comments: passing})
		}
	}
	run.FileReviews = filtered
	return kept, minor, dropped
}

func buildScoringPrompt(run *PipelineRun, memContext string) string {
	var sb strings.Builder
	if memContext != "" {
		sb.WriteString(memContext)
		sb.WriteString("\n")
	}
	// Sanitize + truncate user-controlled fields
	safeTitle := sanitizeUserInput(util.Truncate(run.PREvent.PRTitle, 200, false))
	safeAuthor := sanitizeUserInput(util.Truncate(run.PREvent.PRAuthor, 100, false))
	sb.WriteString(fmt.Sprintf("PR #%d: \"%s\" by %s\n", run.PREvent.PRNumber, safeTitle, safeAuthor))
	if run.PREvent.PRBody != "" {
		sb.WriteString("\n" + wrapInDelimiters("pr_description", sanitizeUserInput(util.Truncate(run.PREvent.PRBody, 1500, false))) + "\n")
	}
	if run.Contract != nil {
		sb.WriteString("\n" + wrapInDelimiters("review_contract", run.Contract.SummaryLine()) + "\n")
		sb.WriteString("Survival thresholds are class-aware: throwaway/docs/generated changes need near-certain findings; migration/security changes are judged MORE sensitively for critical findings. Weigh plausibility against this contract.\n")
	}
	sb.WriteString("\nScore each comment 0-100:\n\n")
	idx := 0
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			specialist := ""
			if c.Specialist != "" {
				specialist = fmt.Sprintf(" [%s]", c.Specialist)
			}
			desc := c.What
			if desc == "" {
				desc = c.Body
			}
			sb.WriteString(fmt.Sprintf("[%d] %s:%d%s — [%s|%s] %s\n", idx, fr.Path, c.Line, specialist, c.Severity, c.Category, desc))
			if c.Suggestion != "" {
				sb.WriteString(fmt.Sprintf("    suggestion: %s\n", c.Suggestion))
			}
			if c.BlastRadius > 0 {
				sb.WriteString(fmt.Sprintf("    blast_radius: This finding affects %d downstream dependents\n", c.BlastRadius))
			}
			if c.Corroboration >= 2 {
				sb.WriteString(fmt.Sprintf("    corroboration: %d specialists independently flagged this (positive signal, context only)\n", c.Corroboration))
			}
			idx++
		}
	}
	sb.WriteString("\nGroup duplicates and score each group. JSON array only:\n[{\"representative\": 0, \"score\": 85, \"severity\": \"critical\", \"duplicates\": [3], \"reason\": \"...\"}]")
	return sb.String()
}

// judgeGroup is the structured output from the LLM sweeper (Layer 4).
type judgeGroup struct {
	Representative int    `json:"representative"`
	Score          int    `json:"score"`
	Severity       string `json:"severity"`
	Duplicates     []int  `json:"duplicates"`
	Reason         string `json:"reason"`
}

// validateJudgeOutput checks that the LLM output is structurally valid:
// 1. All indices in [0, totalFindings)
// 2. No index appears in more than one group
// 3. Coverage >= 80% of findings
func validateJudgeOutput(groups []judgeGroup, totalFindings int) error {
	if len(groups) == 0 {
		return fmt.Errorf("empty judge output")
	}
	seen := make(map[int]bool)
	for gi, g := range groups {
		if g.Representative < 0 || g.Representative >= totalFindings {
			return fmt.Errorf("group %d: representative %d out of range [0,%d)", gi, g.Representative, totalFindings)
		}
		if seen[g.Representative] {
			return fmt.Errorf("group %d: representative %d already claimed", gi, g.Representative)
		}
		seen[g.Representative] = true
		for _, d := range g.Duplicates {
			if d < 0 || d >= totalFindings {
				return fmt.Errorf("group %d: duplicate %d out of range [0,%d)", gi, d, totalFindings)
			}
			if seen[d] {
				return fmt.Errorf("group %d: duplicate %d already claimed", gi, d)
			}
			seen[d] = true
		}
	}
	coverage := float64(len(seen)) / float64(totalFindings)
	if coverage < 0.8 {
		return fmt.Errorf("low coverage: %d/%d findings (%.0f%%) — need ≥80%%", len(seen), totalFindings, coverage*100)
	}
	return nil
}

const scoringSystemPrompt = `You are a code review judge. Your job is to score findings AND group remaining duplicates.

Most deduplication was already handled. You are the final pass — catch any semantic duplicates the deterministic layers missed, and score each group.

For each finding or group of related findings, output:
- representative: index of the best/clearest finding
- score: 0-100 quality score for the group
- severity: override if miscalibrated (critical/warning/suggestion/praise)
- duplicates: indices of findings that are duplicates of the representative (empty array if unique)
- reason: 1-sentence explanation

Scoring guide:
90-100: Definite bug, security flaw, or regression with clear evidence
70-89: Likely valid — minor or context-dependent but technically sound
40-69: Speculative, low-confidence, or tangential
0-39: False positive, style-only, or linter-catchable

Rules:
- Every finding index must appear exactly once (as representative OR in a duplicates array)
- Style-only comments: max 30
- Security with concrete exploit: min 70
- blast_radius > 0: min 70
- Linter-catchable: max 35
- "corroboration: N specialists" is a positive validity signal — lean toward valid, but never score above what the evidence supports
- Survival thresholds are class-aware (see the review contract in the prompt): low-stakes classes need near-certain findings; migration/security classes keep critical findings more readily

Respond ONLY with a JSON array. No other text.
[{"representative": 0, "score": 85, "severity": "critical", "duplicates": [3], "reason": "real SQL injection"}]`

// patternTrustFloor is the empirical-quality cutoff below which a learned
// pattern is treated as distrusted (its matching findings were dismissed
// repeatedly). Mirrors the low-quality band used for pattern decay.
const patternTrustFloor = 0.4

// buildPatternTrustCalibration returns a one-line scoring hint when the
// installation has learned patterns whose quality decayed below
// patternTrustFloor — telling the judge to score findings matching them lower.
// Empty when there are none. One cheap Postgres query, no LLM call.
func buildPatternTrustCalibration(ctx context.Context, st *store.Store, installationID int64) string {
	if st == nil || installationID == 0 {
		return ""
	}
	low, err := st.GetLowQualityPatterns(ctx, installationID, patternTrustFloor, 5)
	if err != nil || len(low) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\n## Pattern Trust\n%d learned pattern(s) have quality <%.1f — developers dismissed matching findings repeatedly. Score findings that match them lower.", len(low), patternTrustFloor)
}

// fetchScoringContext retrieves repo patterns + per-file synthesis from Supermemory to calibrate scoring.
// Non-fatal: returns empty string on any error.
func fetchScoringContext(ctx context.Context, indexer memory.Indexer, owner, repo string, files []FileReview) string {
	if indexer == nil || owner == "" || repo == "" {
		return ""
	}

	repoTag := memory.RepoTagNew(repo)

	// Parallel reads; each SearchHints owns its own 5s timeout + non-fatal Warn
	// (module policy) so a slow search can't stall the scoring pipeline.
	var repoResults []string
	var fileResults []string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		// type=pattern: the unified repo container also holds scenarios,
		// feedback, syntheses, traces and review comments. Scoring calibration
		// ("matching confirmed patterns should score higher") wants learned
		// patterns only — an untyped search dilutes it with unrelated doc types.
		repoResults = indexer.SearchHints(ctx, "confirmed review patterns conventions common issues", repoTag, 5, memory.TypePattern)
	}()
	go func() {
		defer wg.Done()
		if len(files) > 0 {
			var paths []string
			for _, fr := range files {
				paths = append(paths, fr.Path)
			}
			// type=synthesis: "Known File Context" is the file-scoped review
			// history summary; pin the type so scenarios/patterns don't leak in.
			fileResults = indexer.SearchHints(ctx, filePathsQuery("file synthesis ", paths), repoTag, 3, memory.TypeSynthesis)
		}
	}()
	wg.Wait()

	var sb strings.Builder
	if len(fileResults) > 0 {
		sb.WriteString("## Known File Context\n")
		for _, r := range fileResults {
			sb.WriteString("- " + util.Truncate(r, 200, true) + "\n")
		}
		sb.WriteString("\n")
	}

	if len(repoResults) > 0 {
		sb.WriteString(formatMemoryBlock(
			"## Repo Context (from past reviews)\n\n",
			"",
			repoResults,
		))
	}

	if sb.Len() > 0 {
		sb.WriteString("\nUse this context to calibrate scores — issues matching confirmed patterns should score higher.")
	}
	return sb.String()
}

// indexedComment maps a flattened comment index back to its FileReview/Comment position.
type indexedComment struct {
	fileIdx    int
	commentIdx int
}

// scoredComment bundles a FileComment with its file path for FP-detection helpers.
type scoredComment struct {
	FileComment
	FilePath string
}

// severityPromotion describes a rep-severity promotion made by promoteGroupSeverity.
// promoted=false means nothing changed; the caller logs at Info only when true.
type severityPromotion struct {
	promoted bool
	fromSev  Severity
	toSev    Severity
}

// promoteGroupSeverity mutates the judge group's representative comment in place:
//
//  1. Computes the max severity across (rep + duplicates).
//  2. If a duplicate outranks the rep, bumps the rep's Severity to that max so the
//     posted comment carries the correct severity tag. The LLM judge prompt tells
//     it to pick the "best/clearest" rep — nothing in the prompt guarantees it also
//     picks the most-severe one. Severity must be enforced Go-side.
//  3. Appends a short "Also flagged: …" cross-reference to the rep's Why so the
//     author sees the framings of dropped duplicates rather than silently losing
//     them. Truncated per-snippet to 200 chars to bound body growth.
//
// Returns severityPromotion.promoted=true only when the Severity field actually
// changed — merge-ref annotation happens whenever duplicates exist with content,
// regardless of severity change.
func promoteGroupSeverity(run *PipelineRun, g judgeGroup, allComments []indexedComment) severityPromotion {
	if len(g.Duplicates) == 0 {
		return severityPromotion{}
	}
	if g.Representative < 0 || g.Representative >= len(allComments) {
		return severityPromotion{}
	}
	repIC := allComments[g.Representative]
	if repIC.fileIdx >= len(run.FileReviews) ||
		repIC.commentIdx >= len(run.FileReviews[repIC.fileIdx].Comments) {
		return severityPromotion{}
	}
	rep := &run.FileReviews[repIC.fileIdx].Comments[repIC.commentIdx]

	maxSev := rep.Severity
	var mergedRefs []string
	for _, d := range g.Duplicates {
		if d < 0 || d >= len(allComments) {
			continue
		}
		dIC := allComments[d]
		if dIC.fileIdx >= len(run.FileReviews) ||
			dIC.commentIdx >= len(run.FileReviews[dIC.fileIdx].Comments) {
			continue
		}
		dup := run.FileReviews[dIC.fileIdx].Comments[dIC.commentIdx]
		if severityRank(dup.Severity) > severityRank(maxSev) {
			maxSev = dup.Severity
		}
		snippet := strings.TrimSpace(dup.What)
		if snippet == "" {
			snippet = strings.TrimSpace(dup.Body)
		}
		if snippet != "" {
			mergedRefs = append(mergedRefs, fmt.Sprintf(
				"[%s] %s:L%d — %s", dup.Severity,
				run.FileReviews[dIC.fileIdx].Path, dup.Line,
				util.Truncate(snippet, 200, true)))
		}
	}

	out := severityPromotion{}
	if maxSev != rep.Severity {
		out = severityPromotion{promoted: true, fromSev: rep.Severity, toSev: maxSev}
		rep.Severity = maxSev
	}
	if len(mergedRefs) > 0 {
		note := "Also flagged: " + strings.Join(mergedRefs, "; ")
		if rep.Why == "" {
			rep.Why = note
		} else {
			rep.Why = rep.Why + "\n\n" + note
		}
	}
	return out
}

// adjustScores applies deterministic post-LLM caps/floors to known FP/TP patterns.
// Runs after LLM judge scoring, before threshold filtering. Boosts apply first,
// caps last — caps bind regardless of judge score or boost.
func adjustScores(run *PipelineRun, groups []judgeGroup, allComments []indexedComment) {
	for i := range groups {
		g := &groups[i]
		c := getCommentFromIndex(allComments, g.Representative, run)

		// 0. Cross-specialist corroboration: small bounded boost, never a gate.
		if c.Corroboration >= 2 {
			g.Score = min(100, g.Score+10)
			g.Reason += fmt.Sprintf(" [corroborated x%d: +10]", c.Corroboration)
		}

		// 1. Type confusion: "missing await" on non-async function
		if isAwaitFinding(c) && !isAsyncFunction(c) {
			g.Score = min(g.Score, 25)
			g.Reason += " [FP-cap: await-on-non-async]"
		}

		// 2. Threading model error: race condition in single-threaded JS/TS
		if isRaceConditionFinding(c) && isSingleThreadedJS(c) {
			g.Score = min(g.Score, 20)
			g.Reason += " [FP-cap: race-in-single-threaded-js]"
		}

		// 3. Threat model mismatch: attacker framing on internal code
		if isAttackerFraming(c) && isInternalCode(c) {
			g.Score = max(0, g.Score-30)
			g.Reason += " [FP-cap: attacker-framing-internal]"
		}

		// 4. Speculative assertion: no specific code line cited
		if isSpeculativeAssertion(c) {
			g.Score = min(g.Score, 40)
			g.Reason += " [FP-cap: speculative]"
		}

		// 5. SAST corroboration — floor at 75
		if c.SastCorroborated {
			g.Score = max(g.Score, 75)
			g.Reason += " [SAST-corroborated]"
		}

		// 6. Style-ish findings capped at 30 — below every severity threshold,
		// so a style finding can never post no matter what the judge scored it
		// (Review Laws: style is never a finding).
		if isStyleFinding(c) {
			g.Score = min(g.Score, 30)
			g.Reason += " [cap: style]"
		}

		// 7. Generic error_handling capped at 45 unless the file is
		// security-relevant — "add error handling" is the most-dismissed
		// finding class; on auth/token/crypto paths it stays uncapped.
		if c.Category == CategoryErrorHandling && !isSecurityRelevant(strings.ToLower(c.FilePath)) {
			g.Score = min(g.Score, 45)
			g.Reason += " [cap: error-handling]"
		}
	}
}

// isStyleFinding reports whether a finding is style-ish by category or text —
// naming/formatting/import-order commentary that the Review Laws forbid.
func isStyleFinding(c scoredComment) bool {
	if c.Category == CategoryStyle || c.Category == CategoryReadability {
		return true
	}
	lower := strings.ToLower(c.What + " " + c.Body)
	for _, kw := range []string{
		"naming convention", "consider renaming", "code style", "stylistic",
		"formatting", "import order", "import ordering", "indentation",
		"whitespace", "typo",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// getCommentFromIndex resolves a flattened comment index to its scoredComment.
func getCommentFromIndex(allComments []indexedComment, idx int, run *PipelineRun) scoredComment {
	ic := allComments[idx]
	return scoredComment{
		FileComment: run.FileReviews[ic.fileIdx].Comments[ic.commentIdx],
		FilePath:    run.FileReviews[ic.fileIdx].Path,
	}
}

// --- FP-detection helpers (all private, case-insensitive) ---

func isAwaitFinding(c scoredComment) bool {
	lower := strings.ToLower(c.Body)
	return strings.Contains(lower, "missing await") ||
		strings.Contains(lower, "should be awaited") ||
		strings.Contains(lower, "not awaited")
}

func isAsyncFunction(c scoredComment) bool {
	lower := strings.ToLower(c.Body)
	return strings.Contains(lower, "async")
}

func isRaceConditionFinding(c scoredComment) bool {
	lower := strings.ToLower(c.Body)
	return strings.Contains(lower, "race condition") ||
		strings.Contains(lower, "data race") ||
		strings.Contains(lower, "concurrent")
}

func isSingleThreadedJS(c scoredComment) bool {
	lower := strings.ToLower(c.Body)
	ext := strings.ToLower(fileExt(c.FilePath))
	if ext != ".js" && ext != ".ts" && ext != ".tsx" && ext != ".jsx" {
		return false
	}
	return !strings.Contains(lower, "worker") && !strings.Contains(lower, "sharedarraybuffer")
}

func isAttackerFraming(c scoredComment) bool {
	lower := strings.ToLower(c.Body)
	return strings.Contains(lower, "attacker") ||
		strings.Contains(lower, "malicious user") ||
		strings.Contains(lower, "adversary")
}

func isInternalCode(c scoredComment) bool {
	lower := strings.ToLower(c.FilePath)
	return strings.Contains(lower, "internal/") ||
		strings.Contains(lower, "lib/") ||
		strings.Contains(lower, "util/") ||
		strings.Contains(lower, "helper/")
}

func isSpeculativeAssertion(c scoredComment) bool {
	lower := strings.ToLower(c.Body)
	hasSpeculative := strings.Contains(lower, "might") ||
		strings.Contains(lower, "could potentially") ||
		strings.Contains(lower, "may cause")
	if !hasSpeculative {
		return false
	}
	return c.Line == 0
}

// fileExt returns the file extension including the dot, e.g. ".go".
func fileExt(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i:]
		}
		if path[i] == '/' {
			break
		}
	}
	return ""
}

// assignConfidence maps a 0-100 score to a confidence level string.
func assignConfidence(score int) string {
	switch {
	case score >= 80:
		return "high"
	case score >= 65:
		return "medium"
	default:
		return "low"
	}
}
