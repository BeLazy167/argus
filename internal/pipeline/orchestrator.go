package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
	"github.com/BeLazy167/argus/pkg/diff"
)

// splitRepoFullName splits "owner/repo" into its components.
func splitRepoFullName(fullName string) (owner, repo string, err error) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repo name: %s", fullName)
	}
	return parts[0], parts[1], nil
}

// Orchestrator receives PR events and drives them through the review pipeline.
type Orchestrator struct {
	db          *pgxpool.Pool
	st          *store.Store
	ghClient    *ghpkg.Client
	sm          *StateMachine
	reviewStage *ReviewStage
	triageStage *TriageStage
	indexer     *memory.Indexer
	registry    LLMRegistry
	logger      *slog.Logger
}

// LLMRegistry is the subset of llm.Registry used by Orchestrator.
type LLMRegistry interface {
	HasKeyForRepo(ctx context.Context, installationID int64, repoID *int64, providerName string) bool
}

func NewOrchestrator(db *pgxpool.Pool, st *store.Store, ghClient *ghpkg.Client, reviewStage *ReviewStage, triageStage *TriageStage, indexer *memory.Indexer, registry LLMRegistry, logger *slog.Logger) *Orchestrator {
	sm := NewStateMachine(db, logger)

	o := &Orchestrator{
		db:          db,
		st:          st,
		ghClient:    ghClient,
		sm:          sm,
		reviewStage: reviewStage,
		triageStage: triageStage,
		indexer:     indexer,
		registry:    registry,
		logger:      logger,
	}

	sm.RegisterStage(StateTriaging, triageStage.Execute)
	sm.RegisterStage(StateReviewing, reviewStage.Execute)
	sm.RegisterStage(StateSynthesizing, o.synthesize)
	sm.RegisterStage(StatePosting, o.post)

	return o
}

// HandlePREvent processes a pull request webhook event.
func (o *Orchestrator) HandlePREvent(ctx context.Context, event ghpkg.PREvent) error {
	// Only review on opened, synchronize, reopened, manual
	switch event.Action {
	case "opened", "synchronize", "reopened", "manual":
	default:
		o.logger.Info("ignoring PR action", "action", event.Action)
		return nil
	}

	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		return err
	}

	// Auto-register installation + repo on first webhook
	inst, err := o.st.CreateInstallation(ctx, event.InstallationID, owner)
	if err != nil {
		return fmt.Errorf("upserting installation: %w", err)
	}
	dbRepo, err := o.st.UpsertRepo(ctx, inst.ID, event.RepoID, event.RepoFullName, event.BaseRef)
	if err != nil {
		return fmt.Errorf("upserting repo: %w", err)
	}

	if !dbRepo.Enabled {
		o.logger.Info("skipping disabled repo", "repo", event.RepoFullName)
		return nil
	}

	// Check if repo has a model config + API key for the review stage
	if o.registry != nil {
		dbConfigs, _ := o.st.ListModelConfigs(ctx, dbRepo.ID)
		var reviewProvider string
		for _, c := range dbConfigs {
			if c.Stage == string(llm.StageReview) {
				reviewProvider = c.Provider
				break
			}
		}
		if reviewProvider == "" || !o.registry.HasKeyForRepo(ctx, inst.ID, &dbRepo.ID, reviewProvider) {
			o.logger.Info("no API key or model config, posting onboarding comment", "repo", event.RepoFullName, "provider", reviewProvider)
			_ = o.ghClient.CreateIssueComment(ctx, event.InstallationID, owner, repo, event.PRNumber,
				"Welcome to **Argus**! To enable AI code reviews, configure your API key and model at your [Argus Settings](https://argusai.vercel.app/settings).")
			reviewID := uuid.New()
			_, _ = o.db.Exec(ctx, `
				INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, status, trigger, error)
				VALUES ($1, $2, $3, $4, $5, $6, $7, 'failed', 'webhook', 'no_api_key')
			`, reviewID, dbRepo.ID, event.PRNumber, event.PRTitle, event.PRAuthor, event.HeadSHA, event.BaseSHA)
			return nil
		}
	}

	// Fetch diff
	rawDiff, err := o.ghClient.GetPRDiff(ctx, event.InstallationID, owner, repo, event.PRNumber)
	if err != nil {
		return fmt.Errorf("fetching diff: %w", err)
	}

	patchSet, err := diff.Parse(rawDiff)
	if err != nil {
		return fmt.Errorf("parsing diff: %w", err)
	}

	// Check for incremental re-review on synchronize
	var isIncremental bool
	var previousReviewID *uuid.UUID
	if event.Action == "synchronize" {
		prev, err := o.st.GetLastCompletedReview(ctx, dbRepo.ID, event.PRNumber)
		if err == nil && prev != nil {
			// Fetch inter-diff: changes since last reviewed commit
			interDiff, err := o.ghClient.GetCompareCommitsDiff(ctx, event.InstallationID, owner, repo, prev.HeadSHA, event.HeadSHA)
			if err != nil {
				o.logger.Warn("failed to get inter-diff, falling back to full diff", "error", err)
			} else if interDiff != "" {
				interPatch, err := diff.Parse(interDiff)
				if err != nil {
					o.logger.Warn("failed to parse inter-diff, falling back to full diff", "error", err)
				} else {
					patchSet = interPatch
					rawDiff = interDiff
					isIncremental = true
					previousReviewID = &prev.ID
					o.logger.Info("incremental re-review", "previous_head", prev.HeadSHA, "new_head", event.HeadSHA)
				}
			}
		}
	}

	// Create review record
	reviewID := uuid.New()
	_, err = o.db.Exec(ctx, `
		INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, status, trigger)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', 'webhook')
	`, reviewID, dbRepo.ID, event.PRNumber, event.PRTitle, event.PRAuthor, event.HeadSHA, event.BaseSHA)
	if err != nil {
		return fmt.Errorf("creating review record: %w", err)
	}

	run := &PipelineRun{
		ID:               uuid.New(),
		ReviewID:         reviewID,
		State:            StatePending,
		PREvent:          event,
		DBInstallationID: inst.ID,
		DBRepoID:         dbRepo.ID,
		Diff:             patchSet,
		RawDiff:          rawDiff,
		Persona:          loadPersona(dbRepo.SettingsJSON),
		IsIncremental:    isIncremental,
		PreviousReviewID: previousReviewID,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	// Allow command-level persona override
	if event.PersonaOverride != "" {
		p := Persona(event.PersonaOverride)
		if ValidPersonas[p] {
			run.Persona = p
		} else {
			o.logger.Warn("invalid persona override, using repo default",
				"requested", event.PersonaOverride,
				"using", string(run.Persona),
				"repo", event.RepoFullName,
			)
		}
	}

	o.logger.Info("starting review pipeline",
		"review_id", reviewID,
		"repo", event.RepoFullName,
		"pr", event.PRNumber,
		"files", len(patchSet.Files),
		"lines_changed", patchSet.TotalLinesChanged(),
		"incremental", isIncremental,
	)

	return o.sm.Run(ctx, run)
}

// RetryReview resumes a pipeline run for a given review ID.
func (o *Orchestrator) RetryReview(ctx context.Context, reviewID uuid.UUID) error {
	var runID uuid.UUID
	err := o.db.QueryRow(ctx,
		`SELECT id FROM pipeline_states WHERE review_id = $1 ORDER BY updated_at DESC LIMIT 1`,
		reviewID,
	).Scan(&runID)
	if err != nil {
		return fmt.Errorf("finding pipeline run for review %s: %w", reviewID, err)
	}
	_, err = o.sm.Resume(ctx, runID)
	return err
}

func (o *Orchestrator) synthesize(ctx context.Context, run *PipelineRun) error {
	var summary strings.Builder
	if run.IsIncremental {
		summary.WriteString("## Argus Review (Incremental)\n\n")
		summary.WriteString(fmt.Sprintf("Re-reviewed %d changed files with %d new comments.\n\n", len(run.Diff.Files), countComments(run)))
	} else {
		summary.WriteString("## Argus Review\n\n")
		summary.WriteString(fmt.Sprintf("Reviewed %d files with %d comments.\n\n", len(run.Diff.Files), countComments(run)))
	}

	for _, fr := range run.FileReviews {
		summary.WriteString(fmt.Sprintf("### `%s`\n", fr.Path))
		for _, c := range fr.Comments {
			summary.WriteString(fmt.Sprintf("- **[%s]** L%d: %s\n", c.Severity, c.Line, c.Body))
		}
		summary.WriteString("\n")
	}

	run.Synthesis = &SynthesisResult{
		Summary: summary.String(),
		Score:   calculateScore(run),
	}
	return nil
}

func (o *Orchestrator) post(ctx context.Context, run *PipelineRun) error {
	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return err
	}

	submission := &ghpkg.ReviewSubmission{
		Summary: run.Synthesis.Summary,
	}

	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			submission.Comments = append(submission.Comments, ghpkg.ReviewComment{
				Path:      fr.Path,
				Body:      formatCommentBody(c),
				Line:      c.Line,
				StartLine: c.StartLine,
				Side:      "RIGHT",
			})
		}
	}

	ghReviewID, err := o.ghClient.PostReview(
		ctx,
		run.PREvent.InstallationID,
		owner, repo,
		run.PREvent.PRNumber,
		submission,
	)
	if err != nil {
		return fmt.Errorf("posting review: %w", err)
	}

	// Serialize token usage
	var tokenUsageJSON []byte
	if run.Tokens.Total.TotalTokens > 0 {
		tokenUsageJSON, _ = json.Marshal(run.Tokens)
	}

	// Update review record
	_, err = o.db.Exec(ctx, `
		UPDATE reviews SET status = 'completed', github_review_id = $1, summary = $2, score = $3, token_usage = $4, file_count = $5, completed_at = NOW()
		WHERE id = $6
	`, ghReviewID, run.Synthesis.Summary, run.Synthesis.Score, tokenUsageJSON, len(run.FileReviews), run.ReviewID)
	if err != nil {
		return fmt.Errorf("updating review record: %w", err)
	}

	o.logger.Info("posted review", "github_review_id", ghReviewID, "pr", run.PREvent.PRNumber)

	// Persist comments to DB + index in Supermemory (fire-and-forget)
	o.indexComments(ctx, run, ghReviewID)

	return nil
}

func (o *Orchestrator) indexComments(ctx context.Context, run *PipelineRun, ghReviewID int64) {
	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		o.logger.Error("invalid repo name in indexComments", "error", err)
		return
	}

	// Fetch GitHub comment IDs for the review we just posted
	type ghCommentKey struct {
		Path string
		Line int
	}
	ghCommentIDs := make(map[ghCommentKey]int64)
	if ghReviewID > 0 && run.PREvent.InstallationID > 0 {
		ghComments, err := o.ghClient.ListReviewComments(ctx, run.PREvent.InstallationID, owner, repo, run.PREvent.PRNumber, ghReviewID)
		if err != nil {
			o.logger.Error("listing review comments from github", "error", err)
		} else {
			for _, gc := range ghComments {
				line := gc.GetLine()
				if line == 0 {
					line = gc.GetPosition()
				}
				key := ghCommentKey{Path: gc.GetPath(), Line: line}
				ghCommentIDs[key] = gc.GetID()
			}
		}
	}

	side := "RIGHT"
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			sev := string(c.Severity)
			cat := string(c.Category)
			line := c.Line
			var startLine *int
			if c.StartLine > 0 {
				startLine = &c.StartLine
			}

			var snippet *string
			if c.CodeSnippet != "" {
				snippet = &c.CodeSnippet
			}

			var ghCommentID *int64
			if id, ok := ghCommentIDs[ghCommentKey{Path: fr.Path, Line: c.Line}]; ok {
				ghCommentID = &id
			}

			if err := o.st.CreateReviewComment(ctx, run.ReviewID, fr.Path, startLine, &line, &side, c.Body, &sev, &cat, snippet, ghCommentID); err != nil {
				o.logger.Error("persisting review comment", "error", err, "file", fr.Path)
			}

			if o.indexer != nil {
				err := o.indexer.IndexReviewComment(ctx, owner, repo, memory.ReviewMemory{
					ReviewID:    run.ReviewID.String(),
					PRNumber:    run.PREvent.PRNumber,
					FilePath:    fr.Path,
					Body:        c.Body,
					Severity:    sev,
					Category:    cat,
					DiffContext: getDiffContext(run, fr.Path),
				})
				if err != nil {
					o.logger.Error("indexing review comment", "error", err, "file", fr.Path)
				}
			}
		}
	}
}

// truncate returns the first maxLen bytes of s without splitting UTF-8 runes.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	for maxLen > 0 && maxLen < len(s) && s[maxLen]&0xC0 == 0x80 {
		maxLen--
	}
	return s[:maxLen]
}

func getDiffContext(run *PipelineRun, path string) string {
	if run.Diff == nil {
		return ""
	}
	for _, f := range run.Diff.Files {
		if f.NewName == path {
			return truncate(f.RawDiff, 1000)
		}
	}
	return ""
}

// formatCommentBody builds the GitHub review comment body with severity, category, and optional suggestion block.
func formatCommentBody(c FileComment) string {
	body := fmt.Sprintf("**[%s | %s]** %s", c.Severity, c.Category, c.Body)
	if c.Suggestion != "" {
		body += "\n\n```suggestion\n" + strings.TrimRight(c.Suggestion, "\n") + "\n```"
	}
	return body
}

func countComments(run *PipelineRun) int {
	total := 0
	for _, fr := range run.FileReviews {
		total += len(fr.Comments)
	}
	return total
}

func calculateScore(run *PipelineRun) int {
	var criticals, warnings int
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			switch c.Severity {
			case SeverityCritical:
				criticals++
			case SeverityWarning:
				warnings++
			}
		}
	}
	return max(1, 10-criticals*3-warnings)
}

// RecoverIncomplete resumes any in-flight pipeline runs after a restart.
func (o *Orchestrator) RecoverIncomplete(ctx context.Context) error {
	return o.sm.RecoverIncomplete(ctx)
}
