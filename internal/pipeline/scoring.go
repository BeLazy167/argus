package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
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
const scoringThreshold = 80

func (ss *ScoringStage) Execute(ctx context.Context, run *PipelineRun) error {
	// No-op if deep review is off
	if !run.DeepReview {
		return nil
	}

	// Check if scoring model is configured — if not, pass through
	dbConfigs, err := ss.store.ListModelConfigs(ctx, run.DBRepoID)
	if err != nil {
		return fmt.Errorf("loading model configs: %w", err)
	}
	repoConfigs := storeToLLMConfigs(dbConfigs)
	cfg, err := ss.registry.GetConfig(run.DBRepoID, llm.StageScoring, repoConfigs)
	if err != nil {
		slog.Info("no scoring model configured, passing all comments through", "repo_id", run.DBRepoID)
		run.ScoringSkipped = true
		return nil
	}
	provider, err := ss.registry.GetProviderForRepo(ctx, run.DBInstallationID, &run.DBRepoID, cfg.Provider)
	if err != nil {
		slog.Error("scoring provider unavailable, passing all comments through", "error", err)
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

	// Fetch repo memory context for scoring calibration
	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		slog.Warn("scoring: invalid repo name, skipping memory context", "error", err)
	}
	memContext := fetchScoringContext(ctx, ss.memClient, owner, repo, run.FileReviews)

	prompt := buildScoringPrompt(run, memContext)
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      scoringSystemPrompt,
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
	}
	run.Tokens.addToTotal(run.Tokens.Scoring)

	// Parse scored results
	type scoredItem struct {
		Index int `json:"index"`
		Score int `json:"score"`
	}
	scored, err := unmarshalLLMArray[scoredItem](resp.Content)
	if err != nil {
		slog.Error("failed to parse scoring response, keeping all comments", "error", err)
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
	// Truncate user-controlled fields for prompt injection resistance
	safeTitle := truncate(run.PREvent.PRTitle, 200)
	safeAuthor := truncate(run.PREvent.PRAuthor, 100)
	sb.WriteString(fmt.Sprintf("PR #%d: \"%s\" by %s\n\nScore each comment 0-100:\n\n", run.PREvent.PRNumber, safeTitle, safeAuthor))
	idx := 0
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			specialist := ""
			if c.Specialist != "" {
				specialist = fmt.Sprintf(" [%s]", c.Specialist)
			}
			sb.WriteString(fmt.Sprintf("[%d] %s:%d%s — [%s|%s] %s\n", idx, fr.Path, c.Line, specialist, c.Severity, c.Category, c.Body))
			if c.Suggestion != "" {
				sb.WriteString(fmt.Sprintf("    suggestion: %s\n", c.Suggestion))
			}
			idx++
		}
	}
	sb.WriteString("\nRespond with JSON array: [{\"index\": 0, \"score\": 85}, ...]\nScore every comment. JSON array only.")
	return sb.String()
}

const scoringSystemPrompt = `You are a code review quality judge. Score each comment 0-100.

90-100: Definite real bug or security flaw with clear evidence in the diff
70-89: Likely valid issue but may be minor or context-dependent
40-69: Speculative, stylistic, or low-confidence
0-39: False positive, nitpick, or wrong

Deduplication: if multiple comments flag the same issue on the same line, score the BEST version normally, score duplicates 0.

Criteria:
- Is it a real issue? Does the diff actually show the problem?
- Does it explain WHY it matters?
- Is any suggested fix correct?
- Would a senior engineer agree this needs attention?
- If the issue would be caught by a standard linter (ESLint, golint/staticcheck, ruff, clippy), score it no higher than 35 regardless of severity label.

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
		repoResults = searchMemoryContent(searchCtx, memClient, "confirmed review patterns conventions common issues", repoTag, 5)
	}()
	go func() {
		defer wg.Done()
		if len(files) > 0 {
			var paths []string
			for _, fr := range files {
				paths = append(paths, fr.Path)
			}
			fileResults = searchMemoryContent(searchCtx, memClient, filePathsQuery("file synthesis ", paths), repoTag, 3)
		}
	}()
	wg.Wait()

	var sb strings.Builder
	if len(fileResults) > 0 {
		sb.WriteString("## Known File Context\n")
		for _, r := range fileResults {
			sb.WriteString("- " + truncateSnippet(r, 200) + "\n")
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
