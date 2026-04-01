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

// scoringThreshold is the minimum score (0-100) for a comment to survive scoring.
const scoringThreshold = 65

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
	type indexedComment struct {
		fileIdx    int
		commentIdx int
	}
	var allComments []indexedComment
	for fi, fr := range run.FileReviews {
		for ci := range fr.Comments {
			allComments = append(allComments, indexedComment{fileIdx: fi, commentIdx: ci})
		}
	}

	if len(allComments) == 0 {
		return nil
	}

	// Note: dedup is now handled by the dedicated dedupStage before scoring.
	// Re-index for scoring
	allComments = allComments[:0]
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

	// Parse scored results
	type scoredItem struct {
		Index int `json:"index"`
		Score int `json:"score"`
	}
	scored, err := unmarshalLLMArray[scoredItem](resp.Content)
	if err != nil {
		slog.Error("failed to parse scoring response, keeping all comments",
			"error", err,
			"model", cfg.Model,
			"provider", cfg.Provider,
			"finish_reason", resp.FinishReason,
			"response_len", len(resp.Content),
			"response_prefix", util.Truncate(resp.Content, 300, true))
		return nil // non-fatal
	}

	// Build score lookup
	scoreLookup := make(map[int]int, len(scored))
	for _, s := range scored {
		scoreLookup[s.Index] = s.Score
	}

	if len(scored) < len(allComments) {
		slog.Warn("scoring returned fewer items than comments", "scored", len(scored), "expected", len(allComments))
	}

	// Apply scores
	for i, ic := range allComments {
		score, ok := scoreLookup[i]
		if !ok {
			score = 100 // unscored comments default to passing
		}
		run.FileReviews[ic.fileIdx].Comments[ic.commentIdx].Score = score
	}

	// Snapshot all scored comments before filtering — pattern learning uses this
	run.AllFileReviews = make([]FileReview, len(run.FileReviews))
	for i, fr := range run.FileReviews {
		comments := make([]FileComment, len(fr.Comments))
		copy(comments, fr.Comments)
		run.AllFileReviews[i] = FileReview{Path: fr.Path, Comments: comments}
	}

	// Filter comments below threshold
	var kept, dropped int
	filtered := run.FileReviews[:0]
	for _, fr := range run.FileReviews {
		var passing []FileComment
		for _, c := range fr.Comments {
			if c.Score >= scoringThreshold {
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
		for _, ic := range allComments {
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
			"kept":      kept,
			"dropped":   dropped,
			"threshold": scoringThreshold,
		})
	}

	slog.Info("scoring complete", "kept", kept, "dropped", dropped, "threshold", scoringThreshold)
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
	sb.WriteString("\nRespond with JSON array: [{\"index\": 0, \"score\": 85}, ...]\nScore every comment. JSON array only.")
	return sb.String()
}

const scoringSystemPrompt = `You are a code review quality judge. Score each comment 0-100.

Specialist comments (tagged with [bug_hunter], [security], [architecture], [regression], etc.) carry domain-specific intent. Score the finding on its technical merit within that specialty, not whether a generalist would agree.

90-100: Definite real bug, security flaw, or regression with clear evidence in the diff
70-89: Likely valid issue — may be minor or context-dependent, but technically sound within its domain
40-69: Speculative, low-confidence, or only tangentially related to correctness
0-39: False positive, wrong, or duplicate of a higher-scored comment

Style-only comments (naming, formatting, whitespace) that don't affect correctness or readability: maximum score 30.
Security findings with a concrete exploit scenario or clear vulnerability path: minimum score 70.

Deduplication: if multiple comments flag variants of the same root issue, score only the clearest and most actionable one above threshold. Score duplicates at 0.

Criteria:
- Is it a real issue? Does the diff actually show the problem?
- Does it explain WHY it matters?
- Is any suggested fix correct?
- For specialist comments: does it reflect genuine domain expertise?
- If the issue would be caught by a standard linter (ESLint, golint/staticcheck, ruff, clippy), score it no higher than 35 regardless of severity label.
- If a finding has blast_radius > 0, it affects downstream dependents. Score at least 70 regardless of specialist confidence.

Respond ONLY with a JSON array. No other text.`

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

// deduplicateComments groups comments by file path + overlapping line range (within 5 lines),
// then removes near-duplicates (>80% token overlap), keeping the longest comment in each group.
func deduplicateComments(fileReviews []FileReview) []FileReview {
	var result []FileReview
	for _, fr := range fileReviews {
		if len(fr.Comments) <= 1 {
			result = append(result, fr)
			continue
		}

		// Assign each comment to a line-proximity group
		groups := make([]int, len(fr.Comments))
		nextGroup := 0
		for i := range fr.Comments {
			groups[i] = -1
		}
		for i := range fr.Comments {
			if groups[i] >= 0 {
				continue
			}
			groups[i] = nextGroup
			for j := i + 1; j < len(fr.Comments); j++ {
				if groups[j] >= 0 {
					continue
				}
				if linesOverlap(fr.Comments[i].Line, fr.Comments[j].Line, 5) {
					groups[j] = nextGroup
				}
			}
			nextGroup++
		}

		// Within each group, remove duplicates by body similarity
		kept := make([]bool, len(fr.Comments))
		for i := range kept {
			kept[i] = true
		}
		for g := 0; g < nextGroup; g++ {
			var idxs []int
			for i, gi := range groups {
				if gi == g {
					idxs = append(idxs, i)
				}
			}
			for i := 0; i < len(idxs); i++ {
				if !kept[idxs[i]] {
					continue
				}
				for j := i + 1; j < len(idxs); j++ {
					if !kept[idxs[j]] {
						continue
					}
					if tokenSimilarity(fr.Comments[idxs[i]].Body, fr.Comments[idxs[j]].Body) > 0.8 {
						// Keep the longer comment
						if len(fr.Comments[idxs[i]].Body) >= len(fr.Comments[idxs[j]].Body) {
							kept[idxs[j]] = false
						} else {
							kept[idxs[i]] = false
							break
						}
					}
				}
			}
		}

		var passing []FileComment
		for i, c := range fr.Comments {
			if kept[i] {
				passing = append(passing, c)
			}
		}
		if len(passing) > 0 {
			result = append(result, FileReview{Path: fr.Path, Comments: passing})
		}
	}
	return result
}

func linesOverlap(a, b, threshold int) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= threshold
}

// tokenSimilarity returns the ratio of shared tokens to the max token count of two strings.
func tokenSimilarity(a, b string) float64 {
	tokA := strings.Fields(strings.ToLower(a))
	tokB := strings.Fields(strings.ToLower(b))
	if len(tokA) == 0 || len(tokB) == 0 {
		return 0
	}
	set := make(map[string]bool, len(tokA))
	for _, t := range tokA {
		set[t] = true
	}
	shared := 0
	for _, t := range tokB {
		if set[t] {
			shared++
		}
	}
	maxLen := len(tokA)
	if len(tokB) > maxLen {
		maxLen = len(tokB)
	}
	return float64(shared) / float64(maxLen)
}
