package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
	"github.com/BeLazy167/argus/internal/util"
)

// ScoringStage validates review comments using a separate scoring model.
// If no scoring model is configured, it's a no-op (all comments pass through).
type ScoringStage struct {
	registry  *llm.Registry
	store     *store.Store
	memClient *memory.Client
}

func NewScoringStage(registry *llm.Registry, st *store.Store, memClient *memory.Client) *ScoringStage {
	return &ScoringStage{registry: registry, store: st, memClient: memClient}
}

// scoringThresholdForSeverity returns the minimum score for a comment to survive
// scoring, varying by severity. Critical findings use a lower bar; suggestions/info
// require higher confidence.
func scoringThresholdForSeverity(severity string) int {
	switch strings.ToLower(severity) {
	case "critical":
		return 55
	case "warning":
		return 65
	case "suggestion", "info":
		return 75
	default:
		return 65
	}
}

// minSurvivors is the minimum number of comments to keep even if all fall below threshold.
const minSurvivors = 3

func (ss *ScoringStage) Execute(ctx context.Context, run *PipelineRun) error {
	// No-op if deep review is off
	if !run.DeepReview {
		return nil
	}

	// Check if scoring model is configured — if not, pass through
	provider, cfg, err := ss.registry.ResolveProvider(ctx, storeConfigLister{st: ss.store, installationID: run.DBInstallationID}, run.DBInstallationID, run.DBRepoID, llm.StageScoring)
	if err != nil {
		slog.Info("scoring provider unavailable, passing all comments through", "repo_id", run.DBRepoID, "error", err)
		run.ScoringSkipped = true
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
	memContext := fetchScoringContext(ctx, ss.memClient, owner, repo, run.FileReviews)

	prompt := buildScoringPrompt(run, memContext)
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      customOrDefault(run.Prompts, "scoring_system", scoringSystemPrompt),
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   cfg.MaxTokens,
		Temperature: cfg.Temperature,
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
		run.FileReviews[ic.fileIdx].Comments[ic.commentIdx].Score = g.Score
		// Override severity if the judge changed it
		if sev := Severity(g.Severity); ValidSeverities[sev] {
			run.FileReviews[ic.fileIdx].Comments[ic.commentIdx].Severity = sev
		}
		// Track how many were merged by judge
		if len(g.Duplicates) > 0 {
			existing := run.FileReviews[ic.fileIdx].Comments[ic.commentIdx].DedupCount
			if existing == 0 {
				existing = 1
			}
			run.FileReviews[ic.fileIdx].Comments[ic.commentIdx].DedupCount = existing + len(g.Duplicates)
		}
	}
	// Build representative set for O(1) lookup
	repSet := make(map[int]bool, len(groups))
	for _, g := range groups {
		repSet[g.Representative] = true
	}
	// Score unmentioned findings at 100 (pass through)
	var defaultScored int
	for i, ic := range allComments {
		if dupSet[i] || repSet[i] {
			continue
		}
		run.FileReviews[ic.fileIdx].Comments[ic.commentIdx].Score = 100
		defaultScored++
	}
	if defaultScored > 0 {
		slog.Warn("scoring: findings not mentioned by judge assigned default score 100",
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

	// Filter: remove duplicates and comments below threshold
	var kept, dropped, duped int
	filtered := run.FileReviews[:0]
	for fi, fr := range run.FileReviews {
		var passing []FileComment
		for ci, c := range fr.Comments {
			if skipSet[commentKey{fi, ci}] {
				duped++
				continue
			}
			if c.Score >= scoringThresholdForSeverity(string(c.Severity)) {
				passing = append(passing, c)
				kept++
			} else {
				dropped++
			}
		}
		if len(passing) > 0 {
			filtered = append(filtered, FileReview{Path: fr.Path, Comments: passing})
		}
	}

	// MinSurvivors fallback: if all comments were dropped, keep the top-N by score
	if kept == 0 && len(allComments) > 0 {
		// Sort allComments indices by score descending
		type scoredIdx struct {
			fileIdx    int
			commentIdx int
			score      int
		}
		var all []scoredIdx
		for i, ic := range allComments {
			if dupSet[i] {
				continue // don't resurrect judge-marked duplicates
			}
			c := run.AllFileReviews[ic.fileIdx].Comments[ic.commentIdx]
			all = append(all, scoredIdx{fileIdx: ic.fileIdx, commentIdx: ic.commentIdx, score: c.Score})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
		n := minSurvivors
		if n > len(all) {
			n = len(all)
		}
		// Rebuild filtered from top-N using AllFileReviews snapshot
		survivors := make(map[string][]FileComment)
		for _, s := range all[:n] {
			fr := run.AllFileReviews[s.fileIdx]
			survivors[fr.Path] = append(survivors[fr.Path], fr.Comments[s.commentIdx])
		}
		filtered = filtered[:0]
		for path, comments := range survivors {
			filtered = append(filtered, FileReview{Path: path, Comments: comments})
		}
		kept = n
		dropped = len(allComments) - n
		slog.Info("scoring: all comments below threshold, keeping top survivors", "minSurvivors", n)
	}

	run.FileReviews = filtered

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventScoringUpdate, map[string]any{
			"kept":    kept,
			"dropped": dropped,
			"thresholds": map[string]int{
				"critical":   scoringThresholdForSeverity("critical"),
				"warning":    scoringThresholdForSeverity("warning"),
				"suggestion": scoringThresholdForSeverity("suggestion"),
			},
		})
	}

	slog.Info("scoring complete", "kept", kept, "dropped", dropped, "duped_by_judge", duped,
		"threshold_critical", scoringThresholdForSeverity("critical"),
		"threshold_warning", scoringThresholdForSeverity("warning"),
		"threshold_suggestion", scoringThresholdForSeverity("suggestion"))
	return nil
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
	sb.WriteString(fmt.Sprintf("PR #%d: \"%s\" by %s\n\nScore each comment 0-100:\n\n", run.PREvent.PRNumber, safeTitle, safeAuthor))
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

Respond ONLY with a JSON array. No other text.
[{"representative": 0, "score": 85, "severity": "critical", "duplicates": [3], "reason": "real SQL injection"}]`

// fetchScoringContext retrieves repo patterns + per-file synthesis from Supermemory to calibrate scoring.
// Non-fatal: returns empty string on any error.
func fetchScoringContext(ctx context.Context, memClient *memory.Client, owner, repo string, files []FileReview) string {
	if memClient == nil || owner == "" || repo == "" {
		return ""
	}

	repoTag := memory.RepoTag(owner, repo, "patterns")

	// Parallel with timeout to avoid stalling the scoring pipeline
	searchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var repoResults []string
	var fileResults []string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		repoResults = searchMemoryRich(searchCtx, memClient, "confirmed review patterns conventions common issues", repoTag, 5)
	}()
	go func() {
		defer wg.Done()
		if len(files) > 0 {
			var paths []string
			for _, fr := range files {
				paths = append(paths, fr.Path)
			}
			fileResults = searchMemoryRich(searchCtx, memClient, filePathsQuery("file synthesis ", paths), repoTag, 3)
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

// adjustScores applies deterministic post-LLM caps/floors to known FP/TP patterns.
// Runs after LLM judge scoring, before threshold filtering.
func adjustScores(run *PipelineRun, groups []judgeGroup, allComments []indexedComment) {
	for i := range groups {
		g := &groups[i]
		c := getCommentFromIndex(allComments, g.Representative, run)

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
	}
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
