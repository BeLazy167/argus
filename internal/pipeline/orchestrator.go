package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
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
	db           *pgxpool.Pool
	st           *store.Store
	ghClient     *ghpkg.Client
	sm           *StateMachine
	reviewStage  *ReviewStage
	triageStage  *TriageStage
	scoringStage *ScoringStage
	indexer      *memory.Indexer
	registry     LLMRegistry
	eventBus     *EventBus
	logger       *slog.Logger
}

// LLMRegistry is the subset of llm.Registry used by Orchestrator.
type LLMRegistry interface {
	HasKeyForRepo(ctx context.Context, installationID int64, repoID *int64, providerName string) bool
}

func NewOrchestrator(db *pgxpool.Pool, st *store.Store, ghClient *ghpkg.Client, reviewStage *ReviewStage, triageStage *TriageStage, scoringStage *ScoringStage, indexer *memory.Indexer, registry LLMRegistry, eventBus *EventBus, logger *slog.Logger) *Orchestrator {
	sm := NewStateMachine(db, logger)
	sm.eventBus = eventBus

	o := &Orchestrator{
		db:           db,
		st:           st,
		ghClient:     ghClient,
		sm:           sm,
		reviewStage:  reviewStage,
		triageStage:  triageStage,
		scoringStage: scoringStage,
		indexer:      indexer,
		registry:     registry,
		eventBus:     eventBus,
		logger:       logger,
	}

	sm.RegisterStage(StateTriaging, triageStage.Execute)
	sm.RegisterStage(StateReviewing, reviewStage.Execute)
	sm.RegisterStage(StateEnriching, o.enrichFindings)
	sm.RegisterStage(StateScoring, scoringStage.Execute)
	sm.RegisterStage(StatePass2, o.pass2)
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
	var dbConfigs []store.ModelConfig
	if o.registry != nil {
		dbConfigs, err = o.st.ListModelConfigs(ctx, dbRepo.ID)
		if err != nil {
			o.logger.Error("loading model configs for readiness check", "error", err, "repo", event.RepoFullName)
		}
		var reviewProvider string
		for _, c := range dbConfigs {
			if c.Stage == string(llm.StageReview) {
				reviewProvider = c.Provider
				break
			}
		}
		if reviewProvider == "" || !o.registry.HasKeyForRepo(ctx, inst.ID, &dbRepo.ID, reviewProvider) {
			o.logger.Info("no API key or model config, posting onboarding comment", "repo", event.RepoFullName, "provider", reviewProvider)
			if err := o.ghClient.CreateIssueComment(ctx, event.InstallationID, owner, repo, event.PRNumber,
				"Welcome to **Argus**! To enable AI code reviews, configure your API key and model at your [Argus Settings](https://argusai.vercel.app/settings)."); err != nil {
				o.logger.Error("posting onboarding comment", "error", err, "repo", event.RepoFullName)
			}
			reviewID := uuid.New()
			if _, err := o.db.Exec(ctx, `
				INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, status, trigger, error)
				VALUES ($1, $2, $3, $4, $5, $6, $7, 'failed', 'webhook', 'no_api_key')
			`, reviewID, dbRepo.ID, event.PRNumber, event.PRTitle, event.PRAuthor, event.HeadSHA, event.BaseSHA); err != nil {
				o.logger.Error("recording skipped review", "error", err, "repo", event.RepoFullName)
			}
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

	// Auto-resolve stale bot comments on incremental re-push (fire-and-forget)
	if isIncremental {
		go o.autoResolveStaleComments(context.WithoutCancel(ctx), event, patchSet)
	}

	// Create review record
	reviewID := uuid.New()

	// Open SSE topic for live streaming
	if o.eventBus != nil {
		o.eventBus.OpenTopic(reviewID)
		defer o.eventBus.CloseTopic(reviewID)
	}

	_, err = o.db.Exec(ctx, `
		INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, status, trigger, resolved_stale_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', 'webhook', 0)
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
		Persona:             loadPersona(dbRepo.SettingsJSON),
		CustomPersonaPrompt: loadCustomPersonaPrompt(dbRepo.SettingsJSON),
		DeepReview:          isDeepReviewEnabled(dbRepo.SettingsJSON),
		Prompts:          o.loadPrompts(ctx, dbRepo.ID),
		IsIncremental:    isIncremental,
		PreviousReviewID: previousReviewID,
		EventBus:         o.eventBus,
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

	// Post "review started" comment to GitHub
	var reviewModel string
	for _, c := range dbConfigs {
		if c.Stage == string(llm.StageReview) {
			reviewModel = c.Provider + " / " + c.Model
			break
		}
	}
	o.postStartedComment(ctx, event, run, reviewModel)

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

	if o.eventBus != nil {
		o.eventBus.OpenTopic(reviewID)
		defer o.eventBus.CloseTopic(reviewID)
	}

	_, err = o.sm.Resume(ctx, runID)
	return err
}

func (o *Orchestrator) postStartedComment(ctx context.Context, event ghpkg.PREvent, run *PipelineRun, reviewModel string) {
	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		o.logger.Warn("failed to split repo name for started comment", "error", err)
		return
	}

	var rows []string
	if reviewModel != "" {
		rows = append(rows, fmt.Sprintf("| **Model** | `%s` |", reviewModel))
	}
	if run.Persona != "" && run.Persona != PersonaDefault {
		rows = append(rows, fmt.Sprintf("| **Persona** | %s |", strings.ReplaceAll(string(run.Persona), "|", "\\|")))
	}
	if run.DeepReview {
		rows = append(rows, "| **Mode** | Deep review |")
	} else if run.IsIncremental {
		rows = append(rows, "| **Mode** | Incremental |")
	}
	rows = append(rows, fmt.Sprintf("| **Scope** | %d files, ~%d lines |",
		len(run.Diff.Files), run.Diff.TotalLinesChanged()))

	body := fmt.Sprintf("> **Argus** is reviewing this PR — [watch live](https://argusai.vercel.app/reviews/%s)\n\n| | |\n|---|---|\n%s",
		run.ReviewID, strings.Join(rows, "\n"))

	nodeID, err := o.ghClient.CreateIssueCommentWithNodeID(ctx, event.InstallationID, owner, repo, event.PRNumber, body)
	if err != nil {
		o.logger.Warn("failed to post review-started comment", "error", err)
		return
	}
	run.StartedCommentNodeID = nodeID
}

// autoResolveStaleComments resolves bot review threads on files changed in the new push.
func (o *Orchestrator) autoResolveStaleComments(ctx context.Context, event ghpkg.PREvent, patchSet *diff.PatchSet) {
	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		o.logger.Warn("auto-resolve: bad repo name", "error", err)
		return
	}

	threads, err := o.ghClient.ListReviewThreads(ctx, event.InstallationID, owner, repo, event.PRNumber)
	if err != nil {
		o.logger.Warn("auto-resolve: listing threads", "error", err)
		return
	}

	// Build set of changed file paths
	changedFiles := make(map[string]bool)
	for _, f := range patchSet.Files {
		changedFiles[f.NewName] = true
	}

	var resolved int
	for _, t := range threads {
		if t.IsResolved || !strings.HasSuffix(t.AuthorLogin, "[bot]") {
			continue
		}
		if !changedFiles[t.Path] {
			continue
		}
		if err := o.ghClient.ResolveReviewThread(ctx, event.InstallationID, t.ID); err != nil {
			o.logger.Warn("auto-resolve: resolve thread", "error", err, "thread_id", t.ID)
			continue
		}
		resolved++
	}

	if resolved > 0 {
		o.logger.Info("auto-resolved stale comments", "count", resolved, "pr", event.PRNumber)
	}
}

// enrichFindings annotates each comment with pattern/rule matches and novelty flags.
// Non-fatal: defaults to is_new_finding=true on any search failure.
func (o *Orchestrator) enrichFindings(ctx context.Context, run *PipelineRun) error {
	if o.indexer == nil {
		return nil
	}

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return nil // non-fatal
	}

	memClient := o.indexer.Client()
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup

	for i := range run.FileReviews {
		fr := &run.FileReviews[i]
		for j := range fr.Comments {
			c := &fr.Comments[j]
			wg.Add(1)
			sem <- struct{}{}
			go func(c *FileComment) {
				defer wg.Done()
				defer func() { <-sem }()

				_, score := o.indexer.SearchPatternMatch(ctx, owner, repo, c.Body)

				var ruleContent string
				if memClient != nil {
					results := searchMemoryContent(ctx, memClient, c.Body, memory.OwnerTag(owner, "rules"), 1)
					if len(results) > 0 {
						ruleContent = results[0]
					}
				}

				if score > 0.75 {
					c.MatchedPatternScore = score
				}
				if ruleContent != "" {
					c.EnforcedRuleContent = ruleContent
				}
				if score <= 0.75 && ruleContent == "" {
					c.IsNewFinding = true
				}
			}(c)
		}
	}
	wg.Wait()

	o.logger.Info("enriched findings", "repo", run.PREvent.RepoFullName, "pr", run.PREvent.PRNumber)
	return nil
}

func (o *Orchestrator) synthesize(ctx context.Context, run *PipelineRun) error {
	var summary strings.Builder
	header := "## Argus Review\n\n"
	verb := "Reviewed"
	if run.IsIncremental {
		header = "## Argus Review (Incremental)\n\n"
		verb = "Re-reviewed"
	}
	summary.WriteString(header)
	summary.WriteString(fmt.Sprintf("%s %d files with %d comments.\n\n", verb, len(run.Diff.Files), countComments(run)))

	for _, fr := range run.FileReviews {
		summary.WriteString(fmt.Sprintf("### `%s`\n", fr.Path))
		for _, c := range fr.Comments {
			summary.WriteString(fmt.Sprintf("- **[%s]** L%d: %s\n", c.Severity, c.Line, c.Body))
		}
		summary.WriteString("\n")
	}

	// Build concise brief for GitHub review body
	totalComments := countComments(run)
	var brief string
	if totalComments == 0 {
		brief = fmt.Sprintf("Argus reviewed %d files and found no issues.", len(run.Diff.Files))
	} else {
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
		top := topCategories(run, 2)
		brief = fmt.Sprintf("Argus found %d issues (%d critical, %d warnings) across %d files. Key concerns: %s.",
			totalComments, criticals, warnings, len(run.Diff.Files), strings.Join(top, ", "))
	}

	run.Synthesis = &SynthesisResult{
		Summary: summary.String(),
		Brief:   brief,
		Score:   calculateScore(run),
	}

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventSynthesis, map[string]any{
			"summary": run.Synthesis.Summary,
			"score":   run.Synthesis.Score,
		})
	}
	return nil
}

// topCategories returns the top N most frequent comment categories from FileReviews.
func topCategories(run *PipelineRun, n int) []string {
	counts := make(map[Category]int)
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			counts[c.Category]++
		}
	}
	type catCount struct {
		cat   Category
		count int
	}
	var sorted []catCount
	for cat, count := range counts {
		sorted = append(sorted, catCount{cat, count})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
	var result []string
	for i := 0; i < n && i < len(sorted); i++ {
		result = append(result, string(sorted[i].cat))
	}
	return result
}

// pass2 re-reviews "hot" files (3+ comments scored 70+) with a fresh Architecture
// specialist that has no knowledge of prior comments. Implements the Rule of Five:
// a second pass with a different lens catches things the first pass misses.
func (o *Orchestrator) pass2(ctx context.Context, run *PipelineRun) error {
	if !run.DeepReview {
		return nil
	}

	const minCommentsForPass2 = 3
	const minScoreForHot = 70

	// Find hot files — 3+ comments scored 70+
	hotSet := make(map[string]bool)
	for _, fr := range run.FileReviews {
		count := 0
		for _, c := range fr.Comments {
			if c.Score >= minScoreForHot {
				count++
			}
		}
		if count >= minCommentsForPass2 {
			hotSet[fr.Path] = true
		}
	}

	if len(hotSet) == 0 {
		return nil
	}

	o.logger.Info("pass2 triggered", "hot_files", len(hotSet))

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return err
	}

	cfg, provider, err := o.resolveReviewProvider(ctx, run)
	if err != nil {
		o.logger.Warn("pass2 skipped", "error", err)
		return nil
	}

	// Prefetch file contents for hot files
	var hotFiles []diff.FileDiff
	for _, f := range run.Diff.Files {
		if hotSet[f.NewName] {
			hotFiles = append(hotFiles, f)
		}
	}
	fileContents := prefetchFiles(ctx, o.ghClient, run, owner, repo, hotFiles)

	// Fresh Architecture review — no prior comments in context
	for _, f := range hotFiles {
		p := reviewParams{
			file:       f,
			action:     TriageDeep,
			specialist: SpecialistArchitecture,
			systemBase: specialistPrompt(SpecialistArchitecture, run.Prompts),
			deepReview: true,
		}
		rev, tok, err := o.reviewStage.reviewFile(ctx, run, p, fileContents, owner, repo, cfg, provider)
		if err != nil {
			o.logger.Warn("pass2 review failed", "file", f.NewName, "error", err)
			continue
		}
		run.Tokens.Review = append(run.Tokens.Review, tok)
		run.Tokens.addToTotal(tok)

		// Tag as pass2 and merge into existing file reviews
		for i := range rev.Comments {
			rev.Comments[i].Specialist = "pass2_architecture"
		}
		merged := false
		for idx := range run.FileReviews {
			if run.FileReviews[idx].Path == rev.Path {
				run.FileReviews[idx].Comments = append(run.FileReviews[idx].Comments, rev.Comments...)
				merged = true
				break
			}
		}
		if !merged && len(rev.Comments) > 0 {
			run.FileReviews = append(run.FileReviews, rev)
		}
	}

	o.logger.Info("pass2 complete", "files_reviewed", len(hotFiles))
	return nil
}

func (o *Orchestrator) post(ctx context.Context, run *PipelineRun) error {
	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return err
	}

	if run.Synthesis == nil {
		return fmt.Errorf("synthesis result is nil, cannot post review")
	}

	submission := &ghpkg.ReviewSubmission{
		Summary: run.Synthesis.Brief + "\n\n[View full review](https://argusai.vercel.app/reviews/" + run.ReviewID.String() + ")",
	}

	// Build valid-line sets from diff to avoid 422 "line could not be resolved"
	validLines := make(map[string]map[int]bool)
	for _, f := range run.Diff.Files {
		validLines[f.NewName] = f.ValidCommentLines()
	}

	var dropped int
	for _, fr := range run.FileReviews {
		fileValid := validLines[fr.Path]
		for _, c := range fr.Comments {
			if fileValid == nil || !fileValid[c.Line] {
				o.logger.Warn("dropping comment: line not in diff",
					"file", fr.Path, "line", c.Line)
				dropped++
				continue
			}
			startLine := c.StartLine
			if startLine > 0 && !fileValid[startLine] {
				startLine = 0
			}
			submission.Comments = append(submission.Comments, ghpkg.ReviewComment{
				Path:      fr.Path,
				Body:      formatCommentBody(c),
				Line:      c.Line,
				StartLine: startLine,
				Side:      "RIGHT",
			})
		}
	}
	if dropped > 0 {
		o.logger.Warn("dropped comments with lines outside diff", "count", dropped)
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

	// Minimize the "review started" comment now that the full review is posted
	if run.StartedCommentNodeID != "" {
		if err := o.ghClient.MinimizeComment(ctx, run.PREvent.InstallationID, run.StartedCommentNodeID, "RESOLVED"); err != nil {
			o.logger.Warn("failed to minimize started comment", "error", err)
		}
	}

	// Serialize token usage
	var tokenUsageJSON []byte
	if run.Tokens.Total.TotalTokens > 0 {
		if b, err := json.Marshal(run.Tokens); err != nil {
			slog.Warn("failed to marshal token usage", "error", err)
		} else {
			tokenUsageJSON = b
		}
	}

	// Update review record
	persona := strPtrOrNil(string(run.Persona))
	_, err = o.db.Exec(ctx, `
		UPDATE reviews SET status = 'completed', github_review_id = $1, summary = $2, score = $3, token_usage = $4, file_count = $5,
		       deep_review = $6, persona = $7, is_incremental = $8, completed_at = NOW()
		WHERE id = $9
	`, ghReviewID, run.Synthesis.Summary, run.Synthesis.Score, tokenUsageJSON, len(run.FileReviews),
		run.DeepReview, persona, run.IsIncremental, run.ReviewID)
	if err != nil {
		return fmt.Errorf("updating review record: %w", err)
	}

	o.logger.Info("posted review", "github_review_id", ghReviewID, "pr", run.PREvent.PRNumber)

	// Persist comments to DB + index in Supermemory (fire-and-forget)
	o.indexComments(ctx, run, ghReviewID, owner, repo)
	o.indexConfirmedPatterns(ctx, run, owner, repo)
	o.autoLearnPatterns(ctx, run, owner, repo)
	o.extractConventions(ctx, run, owner, repo)
	o.synthesizeFileMemories(ctx, run, owner, repo)
	o.indexPRSummary(ctx, run, owner, repo)

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventCompleted, map[string]any{
			"review_id":      run.ReviewID,
			"total_comments": countComments(run),
			"duration_ms":    time.Since(run.CreatedAt).Milliseconds(),
		})
	}

	return nil
}

// indexConfirmedPatterns saves high-confidence comments as confirmed repo patterns in Supermemory.
// When scoring is available, uses score ≥80 (deep) or ≥90 (non-deep). When skipped, falls back to critical+warning severity.
func (o *Orchestrator) indexConfirmedPatterns(ctx context.Context, run *PipelineRun, owner, repo string) {
	if o.indexer == nil {
		return
	}

	// Deep reviews use lower threshold; non-deep uses a higher bar to compensate for less context
	confirmedThreshold := 80
	if !run.DeepReview {
		confirmedThreshold = 90
	}
	// Use AllFileReviews (pre-scoring snapshot) when available, so pattern learning
	// sees comments that were dropped by the posting threshold but still have valid scores.
	reviews := run.FileReviews
	if len(run.AllFileReviews) > 0 {
		reviews = run.AllFileReviews
	}
	var indexed int
	for _, fr := range reviews {
		for _, c := range fr.Comments {
			var qualifies bool
			if run.ScoringSkipped {
				qualifies = c.Severity == SeverityCritical || c.Severity == SeverityWarning
			} else {
				qualifies = c.Score >= confirmedThreshold
			}
			if !qualifies {
				continue
			}
			indexed++
			content := fmt.Sprintf("Confirmed pattern [%s]: %s (file: %s)", c.Category, c.Body, fr.Path)
			customID := memory.PatternCustomID(owner, repo, "confirmed", content)
			_, err := o.indexer.IndexRepoPattern(ctx, owner, repo, content, customID, map[string]string{
				"source":   "scoring_confirmed",
				"score":    fmt.Sprintf("%d", c.Score),
				"pr":       fmt.Sprintf("%d", run.PREvent.PRNumber),
				"category": string(c.Category),
			})
			if err != nil {
				o.logger.Warn("indexing confirmed pattern", "error", err, "file", fr.Path)
			}
		}
	}
	slog.Info("indexConfirmedPatterns", "indexed", indexed, "scoring_skipped", run.ScoringSkipped)
}

// autoLearnPatterns uses the review LLM to extract 0-3 reusable patterns from high-confidence
// comments. When scoring available, needs 1+ at 75+ (deep) or 80+ (non-deep). When skipped, uses critical+warning severity.
func (o *Orchestrator) autoLearnPatterns(ctx context.Context, run *PipelineRun, owner, repo string) {
	if o.indexer == nil {
		return
	}

	// Collect high-confidence comments (score-based or severity-based fallback)
	// Non-deep reviews use a higher threshold to compensate for less context
	learnThreshold := 75
	if !run.DeepReview {
		learnThreshold = 80
	}
	reviews := run.FileReviews
	if len(run.AllFileReviews) > 0 {
		reviews = run.AllFileReviews
	}
	var highConf []string
	for _, fr := range reviews {
		for _, c := range fr.Comments {
			var qualifies bool
			if run.ScoringSkipped {
				qualifies = c.Severity == SeverityCritical || c.Severity == SeverityWarning
			} else {
				qualifies = c.Score >= learnThreshold
			}
			if qualifies {
				highConf = append(highConf, fmt.Sprintf("[%s|%s] %s:%d — %s", c.Severity, c.Category, fr.Path, c.Line, c.Body))
			}
		}
	}
	// Accept 1+ qualifying comment for pattern extraction
	minRequired := 1
	if len(highConf) < minRequired {
		slog.Info("autoLearnPatterns skipped", "qualifying", len(highConf), "min_required", minRequired, "scoring_skipped", run.ScoringSkipped)
		return
	}
	slog.Info("autoLearnPatterns", "qualifying", len(highConf), "scoring_skipped", run.ScoringSkipped)

	cfg, provider, err := o.resolveReviewProvider(ctx, run)
	if err != nil {
		o.logger.Warn("auto-learn skipped", "error", err)
		return
	}

	prompt := fmt.Sprintf(`From these high-confidence review findings on %s, extract 0-3 reusable patterns SPECIFIC to this codebase (not generic best practices). Each pattern should help catch similar issues in future PRs.

Findings:
%s

Return JSON array: [{"pattern": "description", "category": "bug|security|architecture|regression"}]
Return [] if no repo-specific patterns emerge. JSON array only.`, run.PREvent.RepoFullName, strings.Join(highConf, "\n"))

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      "You extract reusable code review patterns from review findings. Be specific to this codebase.",
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   500,
		Temperature: 0.3,
	})
	if err != nil {
		o.logger.Warn("auto-learn LLM call failed", "error", err)
		return
	}

	type learnedPattern struct {
		Pattern  string `json:"pattern"`
		Category string `json:"category"`
	}
	patterns, err := unmarshalLLMArray[learnedPattern](resp.Content)
	if err != nil {
		o.logger.Warn("auto-learn parse failed", "error", err)
		return
	}
	if len(patterns) > 3 {
		patterns = patterns[:3]
	}

	for _, p := range patterns {
		if p.Pattern == "" {
			continue
		}
		customID := memory.PatternCustomID(owner, repo, "learned", p.Pattern)
		smResp, err := o.indexer.IndexRepoPattern(ctx, owner, repo, p.Pattern, customID, map[string]string{
			"source":   "auto_learn",
			"pr":       fmt.Sprintf("%d", run.PREvent.PRNumber),
			"category": p.Category,
		})
		if err != nil {
			o.logger.Warn("indexing auto-learned pattern", "error", err)
		}
		var smID *string
		if smResp != nil {
			smID = &smResp.ID
		}
		src := "auto_learn"
		cat := strPtrOrNil(p.Category)
		prNum := run.PREvent.PRNumber
		if _, dbErr := o.st.CreatePattern(ctx, run.DBInstallationID, &run.DBRepoID, p.Pattern, smID, strPtrOrNil("argus:auto_learn"), &src, cat, &prNum); dbErr != nil {
			o.logger.Warn("persisting auto-learned pattern", "error", dbErr)
		}
	}

	if len(patterns) > 0 {
		if run.EventBus != nil {
			for _, p := range patterns {
				run.EventBus.Publish(run.ReviewID, EventPatternLearned, map[string]string{
					"pattern":  p.Pattern,
					"category": p.Category,
				})
			}
		}
		o.logger.Info("auto-learned patterns", "count", len(patterns), "repo", run.PREvent.RepoFullName)
	}
}

// extractConventions analyzes the PR diff to identify code style conventions and
// architectural patterns used in the codebase. Unlike autoLearnPatterns (which extracts
// patterns from review comments), this function learns from the code itself —
// capturing what the team writes, not what the reviewer flags.
func (o *Orchestrator) extractConventions(ctx context.Context, run *PipelineRun, owner, repo string) {
	if o.indexer == nil || run.Diff == nil || len(run.Diff.Files) == 0 {
		return
	}

	// Build a compact sample of the diff (first 2000 chars of additions only)
	var diffSample strings.Builder
	for _, f := range run.Diff.Files {
		if diffSample.Len() >= 2000 {
			break
		}
		diffSample.WriteString(fmt.Sprintf("--- %s ---\n", f.NewName))
		for _, line := range strings.Split(f.RawDiff, "\n") {
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				diffSample.WriteString(line + "\n")
				if diffSample.Len() >= 2000 {
					break
				}
			}
		}
	}

	cfg, provider, err := o.resolveReviewProvider(ctx, run)
	if err != nil {
		o.logger.Warn("convention extraction skipped", "error", err)
		return
	}

	convCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`Analyze these code additions from %s and extract 0-3 codebase conventions or architectural patterns.

Focus on:
- Error handling patterns (e.g., "errors wrapped with %%w", "custom error types used")
- Logging conventions (e.g., "structured logging with slog", "log levels follow X pattern")
- Naming conventions (e.g., "handlers suffixed with Handler", "interfaces prefixed with I")
- Architecture patterns (e.g., "repository pattern for DB access", "middleware chain pattern")
- Testing patterns (e.g., "table-driven tests", "test fixtures in testdata/")

Only include patterns that are CLEARLY established (appear multiple times). Skip generic language idioms.

Code additions:
%s

Return JSON array: [{"convention": "description", "category": "style|architecture|error_handling|testing|naming"}]
Return [] if no clear conventions emerge. JSON array only.`, run.PREvent.RepoFullName, diffSample.String())

	resp, err := provider.Complete(convCtx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      "You extract codebase conventions from code diffs. Be specific to this project.",
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   400,
		Temperature: 0.3,
	})
	if err != nil {
		o.logger.Warn("convention extraction LLM failed", "error", err)
		return
	}

	type convention struct {
		Convention string `json:"convention"`
		Category   string `json:"category"`
	}
	conventions, err := unmarshalLLMArray[convention](resp.Content)
	if err != nil {
		o.logger.Warn("convention extraction parse failed", "error", err)
		return
	}
	if len(conventions) > 3 {
		conventions = conventions[:3]
	}

	for _, c := range conventions {
		if c.Convention == "" {
			continue
		}
		content := fmt.Sprintf("Convention [%s]: %s", c.Category, c.Convention)
		customID := memory.PatternCustomID(owner, repo, "convention", c.Convention)
		smResp, err := o.indexer.IndexRepoPattern(ctx, owner, repo, content, customID, map[string]string{
			"source":   "convention_extraction",
			"pr":       fmt.Sprintf("%d", run.PREvent.PRNumber),
			"category": c.Category,
		})
		if err != nil {
			o.logger.Warn("indexing convention", "error", err)
		}
		var smID *string
		if smResp != nil {
			smID = &smResp.ID
		}
		src := "convention"
		cat := strPtrOrNil(c.Category)
		prNum := run.PREvent.PRNumber
		if _, dbErr := o.st.CreatePattern(ctx, run.DBInstallationID, &run.DBRepoID, content, smID, strPtrOrNil("argus:convention"), &src, cat, &prNum); dbErr != nil {
			o.logger.Warn("persisting convention pattern", "error", dbErr)
		}
	}

	if len(conventions) > 0 {
		o.logger.Info("extracted conventions", "count", len(conventions), "repo", run.PREvent.RepoFullName)
	}
}


// synthesizeFileMemories condenses all review comments per file into a single curated memory document.
// Fires for files with signal: 1+ comment scored 60+, or any comment 80+.
// When scoring was skipped, any file with 1+ comment qualifies.
func (o *Orchestrator) synthesizeFileMemories(ctx context.Context, run *PipelineRun, owner, repo string) {
	if o.indexer == nil {
		return
	}

	// Collect qualifying files
	type fileComments struct {
		path     string
		comments []FileComment
	}
	reviews := run.FileReviews
	if len(run.AllFileReviews) > 0 {
		reviews = run.AllFileReviews
	}
	var qualifying []fileComments
	for _, fr := range reviews {
		if run.ScoringSkipped {
			// Scoring unavailable — qualify any file with 1+ comments
			if len(fr.Comments) >= 1 {
				qualifying = append(qualifying, fileComments{path: fr.Path, comments: fr.Comments})
			}
			continue
		}
		count60 := 0
		has80 := false
		for _, c := range fr.Comments {
			if c.Score >= 60 {
				count60++
			}
			if c.Score >= 80 {
				has80 = true
			}
		}
		if count60 >= 1 || has80 {
			qualifying = append(qualifying, fileComments{path: fr.Path, comments: fr.Comments})
		}
	}
	if len(qualifying) == 0 {
		slog.Info("synthesizeFileMemories skipped", "qualifying_files", 0, "scoring_skipped", run.ScoringSkipped)
		return
	}
	slog.Info("synthesizeFileMemories", "qualifying_files", len(qualifying), "scoring_skipped", run.ScoringSkipped)

	// Cap to avoid unbounded LLM calls on large PRs
	const maxSynthesisFiles = 10
	if len(qualifying) > maxSynthesisFiles {
		qualifying = qualifying[:maxSynthesisFiles]
	}

	cfg, provider, err := o.resolveReviewProvider(ctx, run)
	if err != nil {
		o.logger.Warn("synthesis skipped", "error", err)
		return
	}

	const synthesisSystem = `You are synthesizing code review findings into institutional memory.
Given review comments on a single file, produce ONE concise document:

1. Dominant theme (what class of problem does this file have?)
2. Specific patterns to watch for in future reviews
3. What a future reviewer should know before touching this file
4. Any intentional patterns that were explained away

Reference function names and line ranges (not exact numbers — those shift).
Max 200 words. Be concrete.`

	// Timeout to avoid stalling post-pipeline
	synthCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Sanitize + truncate user-controlled fields
	safeTitle := sanitizeUserInput(truncate(run.PREvent.PRTitle, 200))
	safeAuthor := sanitizeUserInput(truncate(run.PREvent.PRAuthor, 100))

	var succeeded, failed int
	for _, fc := range qualifying {
		if synthCtx.Err() != nil {
			o.logger.Warn("synthesis aborted: context cancelled", "succeeded", succeeded, "remaining", len(qualifying)-succeeded-failed)
			return
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("File: %s\nPR #%d: \"%s\" by %s\n\nComments:\n",
			fc.path, run.PREvent.PRNumber, safeTitle, safeAuthor))
		for _, c := range fc.comments {
			sb.WriteString(fmt.Sprintf("[%s|%s] L%d (%s, score:%d) — %s\n",
				c.Severity, c.Category, c.Line, c.Specialist, c.Score, c.Body))
		}

		resp, err := provider.Complete(synthCtx, llm.CompletionRequest{
			Model:       cfg.Model,
			System:      synthesisSystem,
			Messages:    []llm.Message{{Role: "user", Content: sb.String()}},
			MaxTokens:   400,
			Temperature: 0.3,
		})
		if err != nil {
			o.logger.Warn("synthesis LLM failed", "error", err, "file", fc.path)
			failed++
			continue
		}

		customID := memory.SynthesisCustomID(owner, repo, fc.path)
		_, err = o.indexer.IndexRepoPattern(synthCtx, owner, repo, resp.Content, customID, map[string]string{
			"source": "synthesis",
			"pr":     fmt.Sprintf("%d", run.PREvent.PRNumber),
			"file":   fc.path,
		})
		if err != nil {
			o.logger.Warn("indexing file synthesis", "error", err, "file", fc.path)
			failed++
			continue
		}
		succeeded++
	}

	o.logger.Info("synthesized file memories", "succeeded", succeeded, "failed", failed, "repo", run.PREvent.RepoFullName)
}

// indexPRSummary stores a lightweight PR summary in Supermemory for cross-PR context.
// No LLM call — built from existing synthesis output.
func (o *Orchestrator) indexPRSummary(ctx context.Context, run *PipelineRun, owner, repo string) {
	if o.indexer == nil || run.Synthesis == nil {
		return
	}

	var files []string
	for _, fr := range run.FileReviews {
		files = append(files, fr.Path)
	}

	content := fmt.Sprintf("PR #%d \"%s\" by %s\nScore: %d/10\nFiles: %s\n\n%s",
		run.PREvent.PRNumber, run.PREvent.PRTitle, run.PREvent.PRAuthor,
		run.Synthesis.Score, strings.Join(files, ", "),
		truncate(run.Synthesis.Summary, 800))

	customID := memory.PRSummaryCustomID(owner, repo, run.PREvent.PRNumber)
	_, err := o.indexer.IndexRepoPattern(ctx, owner, repo, content, customID, map[string]string{
		"source":    "pr_summary",
		"pr":        fmt.Sprintf("%d", run.PREvent.PRNumber),
		"pr_author": run.PREvent.PRAuthor,
	})
	if err != nil {
		o.logger.Warn("indexing PR summary", "error", err)
	}
}

func (o *Orchestrator) indexComments(ctx context.Context, run *PipelineRun, ghReviewID int64, owner, repo string) {
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

			specialist := strPtrOrNil(string(c.Specialist))
			var confidenceScore *int
			if c.Score > 0 {
				score := c.Score
				confidenceScore = &score
			}

			var matchedPatternScore *float32
			if c.MatchedPatternScore > 0 {
				s := float32(c.MatchedPatternScore)
				matchedPatternScore = &s
			}
			enforcedRule := strPtrOrNil(c.EnforcedRuleContent)

			if err := o.st.CreateReviewComment(ctx, run.ReviewID, fr.Path, startLine, &line, &side, c.Body, &sev, &cat, specialist, snippet, confidenceScore, ghCommentID, nil, matchedPatternScore, enforcedRule, c.IsNewFinding); err != nil {
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

// loadPrompts fetches custom prompt templates for a repo and returns a stage→prompt_text map.
func (o *Orchestrator) loadPrompts(ctx context.Context, repoID int64) map[string]string {
	templates, err := o.st.ListPromptTemplates(ctx, repoID)
	if err != nil {
		o.logger.Warn("loading custom prompts", "error", err)
		return map[string]string{}
	}
	m := make(map[string]string, len(templates))
	for _, t := range templates {
		m[t.Stage] = t.PromptText
	}
	return m
}

// resolveReviewProvider loads model configs and returns a review-stage provider.
func (o *Orchestrator) resolveReviewProvider(ctx context.Context, run *PipelineRun) (llm.ModelConfig, llm.Provider, error) {
	dbConfigs, err := o.st.ListModelConfigs(ctx, run.DBRepoID)
	if err != nil {
		return llm.ModelConfig{}, nil, fmt.Errorf("model configs: %w", err)
	}
	repoConfigs := storeToLLMConfigs(dbConfigs)
	cfg, err := o.reviewStage.registry.GetConfig(run.DBRepoID, llm.StageReview, repoConfigs)
	if err != nil {
		return llm.ModelConfig{}, nil, fmt.Errorf("no review config: %w", err)
	}
	provider, err := o.reviewStage.registry.GetProviderForRepo(ctx, run.DBInstallationID, &run.DBRepoID, cfg.Provider)
	if err != nil {
		return llm.ModelConfig{}, nil, fmt.Errorf("provider unavailable: %w", err)
	}
	return cfg, provider, nil
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

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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
