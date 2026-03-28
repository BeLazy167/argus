package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	gh "github.com/google/go-github/v68/github"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/llm"
	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
	"github.com/BeLazy167/argus/internal/util"
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

// isDiffTooLarge checks if a GitHub API error is a 406 (diff exceeded max lines).
func isDiffTooLarge(err error) bool {
	var ghErr *gh.ErrorResponse
	if errors.As(err, &ghErr) && ghErr.Response != nil {
		return ghErr.Response.StatusCode == http.StatusNotAcceptable
	}
	return false
}

// fetchDiffViaFiles fetches per-file patches when the unified diff is too large.
func (o *Orchestrator) fetchDiffViaFiles(ctx context.Context, event *ghpkg.PREvent, owner, repo string) (*diff.PatchSet, string, error) {
	ghFiles, err := o.ghClient.GetPRFiles(ctx, event.InstallationID, owner, repo, event.PRNumber)
	if err != nil {
		return nil, "", fmt.Errorf("listing PR files: %w", err)
	}

	files := make([]diff.FileInfo, len(ghFiles))
	for i, f := range ghFiles {
		files[i] = diff.FileInfo{
			Name:    f.GetFilename(),
			OldName: f.GetPreviousFilename(),
			Status:  f.GetStatus(),
			Patch:   f.GetPatch(),
		}
	}

	patchSet, err := diff.ParseFromFiles(files)
	if err != nil {
		return nil, "", fmt.Errorf("parsing file patches: %w", err)
	}

	// Fetch full content for large files (patch missing)
	for i, f := range patchSet.Files {
		if f.LargeFile {
			content, fetchErr := o.ghClient.GetFileContent(ctx, event.InstallationID, owner, repo, f.NewName, event.HeadSHA)
			if fetchErr != nil {
				o.logger.Warn("skipping large file content fetch", "file", f.NewName, "error", fetchErr)
				continue
			}
			patchSet.Files[i].FullContent = content
		}
	}

	// Reconstruct a raw diff string from file patches for storage
	var sb strings.Builder
	for _, f := range patchSet.Files {
		if f.RawDiff != "" {
			sb.WriteString(f.RawDiff)
			sb.WriteByte('\n')
		}
	}

	o.logger.Info("fetched diff via files API", "files", len(patchSet.Files), "large_files", patchSet.CountLargeFiles())
	return patchSet, sb.String(), nil
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
	simEngine    *SimulationEngine
	registry     LLMRegistry
	eventBus     *EventBus
	logger       *slog.Logger
}

// LLMRegistry is the subset of llm.Registry used by Orchestrator.
type LLMRegistry interface {
	HasKeyForRepo(ctx context.Context, installationID int64, repoID *int64, providerName string) bool
}

func NewOrchestrator(db *pgxpool.Pool, st *store.Store, ghClient *ghpkg.Client, reviewStage *ReviewStage, triageStage *TriageStage, scoringStage *ScoringStage, indexer *memory.Indexer, registry LLMRegistry, eventBus *EventBus, logger *slog.Logger) *Orchestrator {
	sm := NewStateMachine(db, st, logger)
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
	o.simEngine = NewSimulationEngine(o.reviewStage.registry, st, ghClient, logger)

	sm.RegisterStage(StateTriaging, triageStage.Execute)
	sm.RegisterStage(StateBriefing, o.leadBriefStage)
	sm.RegisterStage(StateReviewing, reviewStage.Execute)
	sm.RegisterStage(StateDeduping, o.dedupStage)
	sm.RegisterStage(StateValidating, o.validateStage)
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
		// continue to review
	case "closed":
		return o.handlePRClosed(ctx, event)
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
				INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, head_ref, status, trigger, error)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'failed', 'webhook', 'no_api_key')
			`, reviewID, dbRepo.ID, event.PRNumber, event.PRTitle, event.PRAuthor, event.HeadSHA, event.BaseSHA, event.HeadRef); err != nil {
				o.logger.Error("recording skipped review", "error", err, "repo", event.RepoFullName)
			}
			return nil
		}
	}

	// Fetch diff — fall back to per-file API if GitHub returns 406 (diff too large)
	rawDiff, err := o.ghClient.GetPRDiff(ctx, event.InstallationID, owner, repo, event.PRNumber)
	var patchSet *diff.PatchSet
	if err != nil && isDiffTooLarge(err) {
		o.logger.Warn("diff too large, falling back to files API", "pr", event.PRNumber, "error", err)
		patchSet, rawDiff, err = o.fetchDiffViaFiles(ctx, &event, owner, repo)
		if err != nil {
			return fmt.Errorf("fallback files API: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("fetching diff: %w", err)
	} else {
		patchSet, err = diff.Parse(rawDiff)
		if err != nil {
			return fmt.Errorf("parsing diff: %w", err)
		}
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
		resolveCtx, resolveCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		go func() {
			defer resolveCancel()
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("autoResolveStaleComments panic", "recover", r, "pr", event.PRNumber)
				}
			}()
			o.autoResolveStaleComments(resolveCtx, event, patchSet)
		}()
	}

	// Create review record
	reviewID := uuid.New()

	// Open SSE topic for live streaming
	if o.eventBus != nil {
		o.eventBus.OpenTopic(reviewID)
		defer o.eventBus.CloseTopic(reviewID)
	}

	_, err = o.db.Exec(ctx, `
		INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, head_ref, status, trigger, resolved_stale_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', 'webhook', 0)
	`, reviewID, dbRepo.ID, event.PRNumber, event.PRTitle, event.PRAuthor, event.HeadSHA, event.BaseSHA, event.HeadRef)
	if err != nil {
		return fmt.Errorf("creating review record: %w", err)
	}

	// Merge org defaults with repo overrides (repo wins)
	mergedSettings, mergedErr := o.st.GetMergedSettings(ctx, inst.ID, dbRepo.ID)
	if mergedErr != nil {
		o.logger.Error("failed to load merged settings, using defaults", "error", mergedErr, "installation", inst.ID, "repo", dbRepo.ID)
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
		Persona:             loadPersona(mergedSettings),
		CustomPersonaPrompt: loadCustomPersonaPrompt(mergedSettings),
		DeepReview:          isDeepReviewEnabled(mergedSettings) && func() bool {
			tier, _ := o.st.GetPlanTier(ctx, inst.ID)
			return tier == "pro"
		}(),
		CrossFileContext: isCrossFileContextEnabled(mergedSettings),
		BlastRadius:     isBlastRadiusEnabled(mergedSettings),
		ScenarioMemory:  isScenarioMemoryEnabled(mergedSettings),
		CodeSimulation:    isCodeSimulationEnabled(mergedSettings),
		PREnrichment:      isPREnrichmentEnabled(mergedSettings),
		LearnPatterns:     isLearnPatternsEnabled(mergedSettings),
		LearnConventions:  isLearnConventionsEnabled(mergedSettings),
		FileSynthesis:     isFileSynthesisEnabled(mergedSettings),
		ArchitectureGraph: isArchitectureGraphEnabled(mergedSettings),
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

func (o *Orchestrator) handlePRClosed(ctx context.Context, event ghpkg.PREvent) error {
	dbRepo, err := o.st.GetRepoByFullName(ctx, event.RepoFullName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			o.logger.Info("[closed] repo not tracked, skipping", "repo", event.RepoFullName)
			return nil
		}
		return fmt.Errorf("handlePRClosed: lookup repo %s: %w", event.RepoFullName, err)
	}
	if !dbRepo.Enabled {
		return nil
	}

	if event.Merged {
		if err := o.st.MarkNodesMerged(ctx, dbRepo.ID, event.PRNumber); err != nil {
			o.logger.Error("[closed] failed to mark nodes merged", "error", err, "pr", event.PRNumber, "repo", event.RepoFullName)
			return nil // non-fatal for webhook response
		}
		o.logger.Info("[closed] PR merged — nodes marked permanent", "pr", event.PRNumber, "repo", event.RepoFullName)
	} else {
		if err := o.st.DeleteUnmergedNodesByPR(ctx, dbRepo.ID, event.PRNumber); err != nil {
			o.logger.Error("[closed] failed to delete unmerged nodes", "error", err, "pr", event.PRNumber, "repo", event.RepoFullName)
			return nil
		}
		o.logger.Info("[closed] PR closed without merge — unmerged nodes removed", "pr", event.PRNumber, "repo", event.RepoFullName)
	}
	return nil
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

	o.logger.Info("auto-resolve: found threads", "total", len(threads), "changed_files", len(changedFiles), "pr", event.PRNumber)

	var resolved, attempted, botUnresolved int
	for _, t := range threads {
		// GraphQL returns app login without "[bot]" suffix (e.g., "argus-eye" not "argus-eye[bot]")
		isBotComment := strings.HasSuffix(t.AuthorLogin, "[bot]") || t.AuthorLogin == "argus-eye"
		if t.IsResolved || !isBotComment {
			continue
		}
		botUnresolved++
		if !changedFiles[t.Path] {
			continue
		}
		attempted++
		if err := o.ghClient.ResolveReviewThread(ctx, event.InstallationID, t.ID); err != nil {
			o.logger.Warn("auto-resolve: resolve thread failed", "error", err, "thread_id", t.ID, "path", t.Path)
			continue
		}
		resolved++
	}

	o.logger.Info("auto-resolve complete", "resolved", resolved, "attempted", attempted, "bot_unresolved", botUnresolved, "pr", event.PRNumber)
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
			go func(c *FileComment, filePath string) {
				defer func() {
					if r := recover(); r != nil {
						o.logger.Error("enrichFindings goroutine panic", "recover", r, "file", filePath)
					}
				}()
				defer wg.Done()
				defer func() { <-sem }()

				// Build a richer query: category + file + body gives Supermemory more semantic signal
				query := fmt.Sprintf("[%s|%s] %s:%d %s", c.Severity, c.Category, filePath, c.Line, c.Body)
				content, score := o.indexer.SearchPatternMatch(ctx, owner, repo, query)

				var ruleContent string
				if memClient != nil {
					results := searchMemoryContent(ctx, memClient, query, memory.OwnerTag(owner, "rules"), 1)
					if len(results) > 0 {
						ruleContent = results[0]
					}
				}

				if score > 0.80 {
					c.MatchedPatternScore = score
					o.logger.Debug("pattern match found", "file", filePath, "line", c.Line, "score", fmt.Sprintf("%.3f", score), "pattern_prefix", util.Truncate(content, 80, true))
				}
				if ruleContent != "" {
					c.EnforcedRuleContent = ruleContent
					o.logger.Debug("rule enforced", "file", filePath, "line", c.Line, "rule_prefix", util.Truncate(ruleContent, 80, true))
				}
				if score <= 0.80 && ruleContent == "" {
					c.IsNewFinding = true
				}
			}(c, fr.Path)
		}
	}
	wg.Wait()

	var matched, enforced, novel int
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.MatchedPatternScore > 0 {
				matched++
			}
			if c.EnforcedRuleContent != "" {
				enforced++
			}
			if c.IsNewFinding {
				novel++
			}
		}
	}
	o.logger.Info("enriched findings", "repo", run.PREvent.RepoFullName, "pr", run.PREvent.PRNumber,
		"total", matched+enforced+novel, "pattern_matches", matched, "rules_enforced", enforced, "new_findings", novel)
	return nil
}

func (o *Orchestrator) synthesize(ctx context.Context, run *PipelineRun) error {
	// Annotate findings with pattern matches and novelty flags before building summary
	if err := o.enrichFindings(ctx, run); err != nil {
		o.logger.Warn("enrichFindings failed, continuing", "error", err)
	}

	var summary strings.Builder
	verb := "Reviewed"
	if run.IsIncremental {
		verb = "Re-reviewed"
	}
	summary.WriteString(fmt.Sprintf("%s %d files with %d comments.\n\n", verb, len(run.Diff.Files), countComments(run)))

	for _, fr := range run.FileReviews {
		summary.WriteString(fmt.Sprintf("### `%s`\n", fr.Path))
		for _, c := range fr.Comments {
			desc := c.What
			if desc == "" {
				desc = c.Body
			}
			summary.WriteString(fmt.Sprintf("- **[%s]** L%d: %s\n", c.Severity, c.Line, desc))
		}
		summary.WriteString("\n")
	}

	score := calculateScore(run)
	totalComments := countComments(run)

	// Run code simulations if enabled
	var simResults []SimulationResult
	if run.CodeSimulation && o.simEngine != nil {
		changedFiles := make([]string, 0, len(run.Diff.Files))
		for _, f := range run.Diff.Files {
			changedFiles = append(changedFiles, f.NewName)
		}
		scenarios, err := FindRelevantScenarios(ctx, o.st, run.DBRepoID, changedFiles)
		if err != nil {
			o.logger.Warn("scenario lookup for simulation failed", "error", err)
		} else if len(scenarios) > 0 {
			simScenarios := make([]SimScenario, len(scenarios))
			for i, s := range scenarios {
				simScenarios[i] = SimScenario{Description: s.Description}
			}
			req := SimulationRequest{Run: run, Scenarios: simScenarios}
			results, simErr := o.simEngine.RunSimulations(ctx, req)
			if simErr != nil {
				o.logger.Warn("simulation failed", "error", simErr)
			} else {
				simResults = results
			}
		}
	}

	// Build concise brief for GitHub review body
	var brief string
	if totalComments == 0 {
		brief = fmt.Sprintf("Argus reviewed %d files and found no issues. Code looks good.", len(run.Diff.Files))
	} else {
		// Try LLM-generated conversational brief
		brief = o.generateConversationalBrief(ctx, run, score)
	}

	run.Synthesis = &SynthesisResult{
		Summary:           summary.String(),
		Brief:             brief,
		Score:             score,
		SimulationResults: simResults,
	}

	if len(simResults) > 0 {
		run.Synthesis.Brief += FormatSimulationResults(simResults)
	}

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventSynthesis, map[string]any{
			"summary": run.Synthesis.Summary,
			"score":   run.Synthesis.Score,
		})
	}
	return nil
}

const synthesisBriefSystemPrompt = `You are writing a short verdict for a pull request. The per-file findings are shown separately below the summary, so do NOT repeat them.

Format (markdown):
**Verdict:** [1-2 sentences: what this PR does and whether it's ready to merge. Mention the most important issue if critical.]

Rules:
- Maximum 2 sentences. Be direct.
- If score >= 8, just say what the PR does and that it looks good.
- If critical issues exist, name the single most important one.
- Do NOT list individual file findings — those are shown per-file below.
- Do NOT include a score, link, file count, or comment count — those are shown separately.
- No greetings, no "hey", no conversational tone.`

// generateConversationalBrief calls the LLM to produce a natural-language summary of the review.
// Falls back to a deterministic brief on failure.
func (o *Orchestrator) generateConversationalBrief(ctx context.Context, run *PipelineRun, score int) string {
	// Build deterministic fallback
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
	fallback := fmt.Sprintf("Argus found %d issues (%d critical, %d warnings) across %d files. Key concerns: %s.",
		countComments(run), criticals, warnings, len(run.Diff.Files), strings.Join(top, ", "))

	// Resolve synthesis provider (falls back to review provider)
	lister := storeConfigLister{st: o.st, installationID: run.DBInstallationID}
	provider, cfg, err := o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageSynthesis)
	if err != nil {
		// Try review stage as fallback
		provider, cfg, err = o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageReview)
		if err != nil {
			o.logger.Warn("synthesis brief: no provider available, using fallback", "error", err)
			return fallback
		}
	}

	prompt := buildSynthesisBriefPrompt(run, score)
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      synthesisBriefSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   400,
		Temperature: 0.7,
	})
	if err != nil {
		o.logger.Warn("synthesis brief LLM call failed, using fallback", "error", err)
		return fallback
	}

	run.Tokens.Synthesis = StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}
	run.Tokens.addToTotal(run.Tokens.Synthesis)
	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventTokenUpdate, map[string]any{
			"total_tokens": run.Tokens.Total.TotalTokens,
			"cost":         run.Tokens.Total.Cost,
		})
	}

	brief := strings.TrimSpace(resp.Content)
	if brief == "" {
		return fallback
	}
	return util.Truncate(brief, 1500, false)
}

func buildSynthesisBriefPrompt(run *PipelineRun, score int) string {
	var sb strings.Builder
	safeTitle := sanitizeUserInput(util.Truncate(run.PREvent.PRTitle, 200, false))
	safeAuthor := sanitizeUserInput(util.Truncate(run.PREvent.PRAuthor, 100, false))
	sb.WriteString(fmt.Sprintf("PR #%d: \"%s\" by %s\n", run.PREvent.PRNumber, safeTitle, safeAuthor))
	sb.WriteString(fmt.Sprintf("Files reviewed: %d, Score: %d/10\n\n", len(run.Diff.Files), score))

	if run.PREvent.PRBody != "" {
		sb.WriteString(wrapInDelimiters("pr_description", sanitizeUserInput(util.Truncate(run.PREvent.PRBody, 300, false))) + "\n\n")
	}

	// Only pass severity counts — NOT individual findings (those are shown per-file)
	var criticals, warnings, suggestions int
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			switch c.Severity {
			case SeverityCritical:
				criticals++
			case SeverityWarning:
				warnings++
			case SeveritySuggestion:
				suggestions++
			}
		}
	}
	sb.WriteString(fmt.Sprintf("Severity breakdown: %d critical, %d warning, %d suggestion\n", criticals, warnings, suggestions))
	sb.WriteString("\nWrite a short verdict following the system prompt format. Focus on WHAT the PR does and WHETHER it's ready.")
	return sb.String()
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
	pass2Ctx, pass2Cancel := context.WithTimeout(ctx, 60*time.Second)
	defer pass2Cancel()

	var completed int
	for _, f := range hotFiles {
		if pass2Ctx.Err() != nil {
			o.logger.Warn("pass2 timeout — returning partial results", "completed", completed, "total", len(hotFiles))
			break
		}
		p := reviewParams{
			file:       f,
			action:     TriageDeep,
			specialist: SpecialistArchitecture,
			systemBase: specialistPrompt(SpecialistArchitecture, run.Prompts),
			deepReview: true,
		}
		rev, tok, err := o.reviewStage.reviewFile(pass2Ctx, run, p, fileContents, owner, repo, cfg, provider)
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
		completed++
	}

	o.logger.Info("pass2 complete", "files_reviewed", completed, "total_hot", len(hotFiles))

	// Re-validate all comments against diff to prevent 422 from GitHub
	validLines := make(map[string]map[int]bool)
	for _, f := range run.Diff.Files {
		validLines[f.NewName] = f.ValidCommentLines()
	}
	var cleaned []FileReview
	var droppedAfterPass2 int
	for _, fr := range run.FileReviews {
		fileValid := validLines[fr.Path]
		var validComments []FileComment
		for _, c := range fr.Comments {
			if fileValid == nil || !fileValid[c.Line] {
				droppedAfterPass2++
				continue
			}
			if c.StartLine > 0 && !fileValid[c.StartLine] {
				c.StartLine = 0
			}
			validComments = append(validComments, c)
		}
		if len(validComments) > 0 {
			cleaned = append(cleaned, FileReview{Path: fr.Path, Comments: validComments})
		}
	}
	if droppedAfterPass2 > 0 {
		o.logger.Warn("dropped invalid comments after pass2", "count", droppedAfterPass2, "pr", run.PREvent.PRNumber)
	}
	run.FileReviews = cleaned
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

	reviewHeader := "## Argus Review\n\n"
	if run.IsIncremental {
		reviewHeader = "## Argus Review (Incremental)\n\n"
	}
	submission := &ghpkg.ReviewSubmission{
		Summary: reviewHeader + run.Synthesis.Brief +
			fmt.Sprintf("\n\nScore: **%d/10** · [Full review →](https://argusai.vercel.app/reviews/%s)", run.Synthesis.Score, run.ReviewID.String()),
		HeadSHA: run.PREvent.HeadSHA,
	}

	// Build valid-line sets from diff to avoid 422 "line could not be resolved"
	validLines := make(map[string]map[int]bool)
	for _, f := range run.Diff.Files {
		validLines[f.NewName] = f.ValidCommentLines()
	}

	var fileLevelCount int
	for _, fr := range run.FileReviews {
		fileValid := validLines[fr.Path]
		for _, c := range fr.Comments {
			if fileValid == nil || !fileValid[c.Line] {
				// Can't post inline — post as file-level comment instead
				submission.Comments = append(submission.Comments, ghpkg.ReviewComment{
					Path:        fr.Path,
					Body:        fmt.Sprintf("**L%d** — %s", c.Line, formatCommentBody(c)),
					SubjectType: "file",
				})
				fileLevelCount++
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
	if fileLevelCount > 0 {
		o.logger.Info("posting file-level comments for lines outside diff", "count", fileLevelCount)
	}

	ghReviewID, err := o.ghClient.PostReview(
		ctx,
		run.PREvent.InstallationID,
		owner, repo,
		run.PREvent.PRNumber,
		submission,
	)
	if err != nil && strings.Contains(err.Error(), "422") {
		// Retry without multi-line start positions (GitHub can't resolve them)
		o.logger.Warn("posting review failed with 422, retrying without start_line", "error", err, "pr", run.PREvent.PRNumber)
		for i := range submission.Comments {
			submission.Comments[i].StartLine = 0
		}
		ghReviewID, err = o.ghClient.PostReview(
			ctx,
			run.PREvent.InstallationID,
			owner, repo,
			run.PREvent.PRNumber,
			submission,
		)
	}
	// Persist review data BEFORE posting to GitHub so a 502 doesn't lose results
	var tokenUsageJSON []byte
	if run.Tokens.Total.TotalTokens > 0 {
		if b, err := json.Marshal(run.Tokens); err != nil {
			slog.Warn("failed to marshal token usage", "error", err)
		} else {
			tokenUsageJSON = b
		}
	}
	persona := strPtrOrNil(string(run.Persona))
	simResultsJSON, simErr := json.Marshal(run.Synthesis.SimulationResults)
	if simErr != nil {
		o.logger.Warn("failed to marshal simulation results", "error", simErr)
		simResultsJSON = nil
	}
	_, dbErr := o.db.Exec(ctx, `
		UPDATE reviews SET summary = $1, score = $2, token_usage = $3, file_count = $4,
		       deep_review = $5, persona = $6, is_incremental = $7, simulation_results = $8
		WHERE id = $9
	`, run.Synthesis.Summary, run.Synthesis.Score, tokenUsageJSON, len(run.FileReviews),
		run.DeepReview, persona, run.IsIncremental, simResultsJSON, run.ReviewID)
	if dbErr != nil {
		o.logger.Warn("pre-post DB update failed", "error", dbErr)
	}

	if err != nil {
		return fmt.Errorf("posting review: %w", err)
	}

	// Minimize the "review started" comment now that the full review is posted
	if run.StartedCommentNodeID != "" {
		if err := o.ghClient.MinimizeComment(ctx, run.PREvent.InstallationID, run.StartedCommentNodeID, "RESOLVED"); err != nil {
			o.logger.Warn("failed to minimize started comment", "error", err)
		}
	}

	// Mark completed + store github_review_id
	_, err = o.db.Exec(ctx, `
		UPDATE reviews SET status = 'completed', github_review_id = $1, completed_at = NOW()
		WHERE id = $2
	`, ghReviewID, run.ReviewID)
	if err != nil {
		return fmt.Errorf("updating review record: %w", err)
	}

	o.logger.Info("[posted] review", "github_review_id", ghReviewID, "pr", run.PREvent.PRNumber,
		"comments", countComments(run), "files", len(run.FileReviews), "score", run.Synthesis.Score,
		"deep_review", run.DeepReview, "duration_ms", time.Since(run.CreatedAt).Milliseconds())

	// Persist comments to DB + index in Supermemory
	o.indexComments(ctx, run, ghReviewID, owner, repo)
	o.indexConfirmedPatterns(ctx, run, owner, repo)

	// Post-review ops use detached context — pipeline is done, these are best-effort
	postCtx := context.WithoutCancel(ctx)

	if run.LearnPatterns {
		func() {
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("post-review panic", "recover", r, "op", "autoLearnPatterns", "pr", run.PREvent.PRNumber)
				}
			}()
			o.autoLearnPatterns(postCtx, run, owner, repo)
		}()
	}
	if run.LearnConventions {
		func() {
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("post-review panic", "recover", r, "op", "extractConventions", "pr", run.PREvent.PRNumber)
				}
			}()
			o.extractConventions(postCtx, run, owner, repo)
		}()
	}
	if run.FileSynthesis {
		func() {
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("post-review panic", "recover", r, "op", "synthesizeFileMemories", "pr", run.PREvent.PRNumber)
				}
			}()
			o.synthesizeFileMemories(postCtx, run, owner, repo)
		}()
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				o.logger.Error("post-review panic", "recover", r, "op", "indexPRSummary", "pr", run.PREvent.PRNumber)
			}
		}()
		o.indexPRSummary(postCtx, run, owner, repo)
	}()
	if run.ArchitectureGraph {
		func() {
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("post-review panic", "recover", r, "op", "extractArchitectureGraph", "pr", run.PREvent.PRNumber)
				}
			}()
			o.extractArchitectureGraph(postCtx, run, owner, repo)
		}()
	}
	if run.PREnrichment {
		func() {
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("enrichPRDescription panic", "recover", r, "pr", run.PREvent.PRNumber)
				}
			}()
			o.enrichPRDescription(postCtx, run, owner, repo)
		}()
	}

	// Persist final token usage including post-review ops
	if run.Tokens.Total.TotalTokens > 0 {
		if b, err := json.Marshal(run.Tokens); err == nil {
			if _, err := o.db.Exec(ctx, `UPDATE reviews SET token_usage = $1 WHERE id = $2`, b, run.ReviewID); err != nil {
				o.logger.Warn("failed to persist post-review tokens", "error", err)
			}
		}
	}

	// Collect changed file paths once for simulation indexing + scenario outdating
	changedPaths := make([]string, 0, len(run.Diff.Files))
	for _, f := range run.Diff.Files {
		changedPaths = append(changedPaths, f.NewName)
	}

	if len(run.Synthesis.SimulationResults) > 0 && o.indexer != nil {
		for _, result := range run.Synthesis.SimulationResults {
			if err := o.indexer.IndexSimulationResult(ctx, owner, repo, run.PREvent.PRNumber, changedPaths,
				result.Passes, result.Scenario, result.Confidence, result.RootCause, result.Impact); err != nil {
				o.logger.Warn("indexing simulation result", "error", err)
			}
			if !result.Passes && result.Confidence >= 0.5 {
				matches := o.indexer.SearchScenariosWithIDs(ctx, owner, repo, result.Scenario, "", 1)
				if len(matches) > 0 && matches[0].Similarity >= 0.75 {
					if err := o.st.Q.IncrementScenarioTriggerCount(ctx, matches[0].ID); err != nil {
						o.logger.Warn("incrementing scenario trigger count", "error", err)
					}
				}
			}
		}
	}

	// Mark scenarios touching changed files as outdated
	if err := o.st.MarkScenarioOutdated(ctx, run.DBRepoID, changedPaths); err != nil {
		o.logger.Warn("marking scenarios outdated", "error", err, "pr", run.PREvent.PRNumber)
	}

	// Collect decision traces (auto-indexed — observational, not actionable)
	traceSeeds := CollectReviewTraces(run)
	var traceFails int
	for _, seed := range traceSeeds {
		if err := o.st.CreateTrace(ctx, run.DBRepoID, seed.FilePath, seed.SymbolName, seed.TraceType, seed.Content, seed.Severity, seed.ReviewID, seed.PRNumber, seed.Metadata); err != nil {
			traceFails++
		}
		if err := o.indexer.IndexDecisionTrace(ctx, owner, repo, seed.FilePath, seed.TraceType, seed.Content, seed.Severity); err != nil {
			o.logger.Warn("indexing decision trace", "error", err, "file", seed.FilePath)
		}
	}
	if traceFails > 0 {
		o.logger.Warn("some decision traces failed to persist", "failed", traceFails, "total", len(traceSeeds))
	}

	// Auto-learn scenarios from critical/warning findings (gated by feature flag).
	if run.ScenarioMemory {
		scenarioSeeds := ExtractScenariosFromReview(run)
		StoreScenarioSeeds(ctx, o.st, o.indexer, owner, repo, run.DBInstallationID, &run.DBRepoID, scenarioSeeds)
	}

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventCompleted, map[string]any{
			"review_id":      run.ReviewID,
			"total_comments": countComments(run),
			"duration_ms":    time.Since(run.CreatedAt).Milliseconds(),
		})
	}

	return nil
}

const (
	enrichmentStartMarker = "<!-- argus-enrichment-start -->"
	enrichmentEndMarker   = "<!-- argus-enrichment-end -->"
)

const enrichmentSystemPrompt = `You help complete a PR description by adding what the author missed or forgot. Think of yourself as a helpful co-author — you read the code changes and add the parts the author didn't write.

Rules:
- If the PR description is EMPTY: write a complete summary of what this PR does (3-6 bullet points covering key changes).
- If the PR description is PARTIAL: only add bullet points for features/changes the author didn't mention. Match their style and tone.
- If the PR description already covers everything: return empty arrays.
- Write from the author's perspective ("Adds...", "Updates...", "Introduces...") — NOT as a reviewer.
- Focus on WHAT the code does, not bugs or issues. No security warnings, no criticism.
- Keep each point to one concise sentence.

Also generate a clean Mermaid diagram showing how the changed components relate. Keep it simple (max 8 nodes). Use flowchart for data flow, graph TD for architecture.

Respond with JSON only:
{
  "missing_points": ["Adds batch processing with configurable concurrency and retry logic", "Introduces fuzzy search with Levenshtein distance scoring"],
  "diagram": "flowchart LR\n  A[API] --> B[Processor]",
  "diagram_title": "Architecture"
}`

// enrichPRDescription fills in missing parts of the PR description as a co-author.
// If empty, writes a full summary. If partial, adds what the author missed.
func (o *Orchestrator) enrichPRDescription(ctx context.Context, run *PipelineRun, owner, repo string) {
	if run.Synthesis == nil || len(run.FileReviews) == 0 {
		return
	}

	// Resolve provider
	lister := storeConfigLister{st: o.st, installationID: run.DBInstallationID}
	provider, cfg, err := o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageSynthesis)
	if err != nil {
		o.logger.Warn("enrichPRDescription: synthesis provider unavailable, trying review", "error", err)
		provider, cfg, err = o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageReview)
		if err != nil {
			o.logger.Warn("enrichPRDescription: no provider", "error", err)
			return
		}
	}

	prompt := buildEnrichmentPrompt(run)
	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      enrichmentSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   500,
		Temperature: 0.3,
	})
	if err != nil {
		o.logger.Warn("enrichPRDescription: LLM call failed", "error", err)
		return
	}
	run.Tokens.Enrichment = StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}
	run.Tokens.addToTotal(run.Tokens.Enrichment)
	if strings.TrimSpace(resp.Content) == "" {
		o.logger.Warn("enrichPRDescription: empty LLM response")
		return
	}

	var result struct {
		MissingPoints []string `json:"missing_points"`
		Diagram       string   `json:"diagram"`
		DiagramTitle  string   `json:"diagram_title"`
	}
	cleaned := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		o.logger.Warn("enrichPRDescription: failed to parse LLM response", "error", err, "response_prefix", util.Truncate(resp.Content, 200, true))
		return
	}

	if len(result.MissingPoints) == 0 && result.Diagram == "" {
		o.logger.Info("enrichPRDescription: nothing missing, skipping")
		return
	}

	// Build enrichment section
	var section strings.Builder
	section.WriteString(enrichmentStartMarker + "\n\n")
	if len(result.MissingPoints) > 0 {
		section.WriteString("**Also in this PR:**\n")
		for _, p := range result.MissingPoints {
			section.WriteString(fmt.Sprintf("- %s\n", sanitizeUserInput(p)))
		}
		section.WriteString("\n")
	}
	if result.Diagram != "" && isValidMermaid(result.Diagram) {
		// Save diagram to review record
		if _, err := o.db.Exec(ctx, `UPDATE reviews SET diagram = $1, diagram_title = $2 WHERE id = $3`,
			result.Diagram, result.DiagramTitle, run.ReviewID); err != nil {
			o.logger.Warn("failed to save diagram", "error", err)
		}
		section.WriteString("<details>\n")
		title := "Architecture"
		if result.DiagramTitle != "" {
			title = sanitizeUserInput(result.DiagramTitle)
		}
		section.WriteString(fmt.Sprintf("<summary>%s</summary>\n\n", title))
		section.WriteString("```mermaid\n" + result.Diagram + "\n```\n")
		section.WriteString("</details>\n")
	}
	section.WriteString("\n<sub>Auto-enriched by [Argus](https://argusai.vercel.app)</sub>\n")
	section.WriteString(enrichmentEndMarker)

	// Fetch current PR body (may have been edited since webhook)
	prEvent, fetchErr := o.ghClient.GetPullRequest(ctx, run.PREvent.InstallationID, owner, repo, run.PREvent.PRNumber)
	if fetchErr != nil {
		o.logger.Warn("enrichPRDescription: failed to fetch PR", "error", fetchErr)
		return
	}
	body := prEvent.PRBody

	// Replace existing enrichment or append
	newBody := replaceOrAppendSection(body, enrichmentStartMarker, enrichmentEndMarker, section.String())

	if err := o.ghClient.UpdatePRDescription(ctx, run.PREvent.InstallationID, owner, repo, run.PREvent.PRNumber, newBody); err != nil {
		o.logger.Warn("enrichPRDescription: failed to update PR", "error", err)
		return
	}
	o.logger.Info("enriched PR description", "pr", run.PREvent.PRNumber)
}

// replaceOrAppendSection replaces content between markers or appends if not found.
func replaceOrAppendSection(body, startMarker, endMarker, section string) string {
	startIdx := strings.Index(body, startMarker)
	endIdx := strings.Index(body, endMarker)
	if startIdx >= 0 && endIdx >= 0 && startIdx < endIdx {
		return body[:startIdx] + section + body[endIdx+len(endMarker):]
	}
	return strings.TrimRight(body, "\n") + "\n\n" + section
}

// isValidMermaid does a basic syntax check on mermaid diagram text.
// Checks balanced brackets/pipes and rejects common LLM syntax errors.
func isValidMermaid(diagram string) bool {
	var squares, parens, braces, pipes int
	for _, c := range diagram {
		switch c {
		case '[':
			squares++
		case ']':
			squares--
		case '(':
			parens++
		case ')':
			parens--
		case '{':
			braces++
		case '}':
			braces--
		case '|':
			pipes++
		}
		if squares < 0 || parens < 0 || braces < 0 {
			return false
		}
	}
	return squares == 0 && parens == 0 && braces == 0 && pipes%2 == 0
}

func buildEnrichmentPrompt(run *PipelineRun) string {
	var sb strings.Builder
	safeTitle := sanitizeUserInput(util.Truncate(run.PREvent.PRTitle, 200, false))
	sb.WriteString(fmt.Sprintf("## PR #%d: %s\n\n", run.PREvent.PRNumber, safeTitle))

	sb.WriteString("### PR Description (what the author says this PR does):\n")
	if run.PREvent.PRBody != "" {
		sb.WriteString(sanitizeUserInput(util.Truncate(run.PREvent.PRBody, 2000, false)))
	} else {
		sb.WriteString("(empty — no description provided)")
	}
	sb.WriteString("\n\n### Actual changes found by code review:\n")

	for _, fr := range run.FileReviews {
		sb.WriteString(fmt.Sprintf("**%s**\n", fr.Path))
		for _, c := range fr.Comments {
			what := c.What
			if what == "" {
				what = util.Truncate(c.Body, 100, true)
			}
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", c.Severity, what))
		}
	}

	sb.WriteString("\n### Changed files:\n")
	for _, f := range run.Diff.Files {
		status := string(f.Status)
		if f.LargeFile {
			status += " (large)"
		}
		sb.WriteString(fmt.Sprintf("- %s (%s)\n", f.NewName, status))
	}

	return sb.String()
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

// isGenericPattern returns true if the pattern doesn't reference file paths from the PR diff.
// Generic patterns are promoted to org-level so they apply across all repos.
func isGenericPattern(pattern string, patchSet *diff.PatchSet) bool {
	if patchSet == nil {
		return false
	}
	patternLower := strings.ToLower(pattern)
	for _, f := range patchSet.Files {
		if strings.Contains(patternLower, strings.ToLower(f.NewName)) {
			return false
		}
		baseName := path.Base(f.NewName)
		if len(baseName) > 3 && strings.Contains(patternLower, strings.ToLower(baseName)) {
			return false
		}
	}
	return true
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
	run.Tokens.Patterns = StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}
	run.Tokens.addToTotal(run.Tokens.Patterns)

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

		// Also store as org-level if pattern is generic (doesn't reference repo-specific file paths)
		if isGenericPattern(p.Pattern, run.Diff) {
			orgCustomID := memory.PatternCustomID(owner, "", "org_learned", p.Pattern)
			var orgSmID *string
			orgResp, orgErr := o.indexer.IndexOwnerPattern(ctx, owner, p.Pattern, orgCustomID, map[string]string{
				"source":   "auto_learn",
				"pr":       fmt.Sprintf("%d", run.PREvent.PRNumber),
				"category": p.Category,
				"repo":     run.PREvent.RepoFullName,
			})
			if orgErr != nil {
				o.logger.Warn("indexing org pattern", "error", orgErr)
			} else if orgResp != nil {
				orgSmID = &orgResp.ID
			}
			if _, dbErr := o.st.CreatePattern(ctx, run.DBInstallationID, nil, p.Pattern, orgSmID, strPtrOrNil("argus:auto_learn"), &src, cat, &prNum); dbErr != nil {
				o.logger.Warn("persisting org-level pattern", "error", dbErr)
			}
			o.logger.Info("promoted pattern to org level", "pattern", util.Truncate(p.Pattern, 80, true))
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
	run.Tokens.Conventions = StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}
	run.Tokens.addToTotal(run.Tokens.Conventions)

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
	safeTitle := sanitizeUserInput(util.Truncate(run.PREvent.PRTitle, 200, false))
	safeAuthor := sanitizeUserInput(util.Truncate(run.PREvent.PRAuthor, 100, false))

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
		fileTok := StageTokens{
			PromptTokens:     resp.TokensUsed.PromptTokens,
			CompletionTokens: resp.TokensUsed.CompletionTokens,
			TotalTokens:      resp.TokensUsed.TotalTokens,
			Cost:             resp.Cost,
			Model:            cfg.Model,
			Provider:         cfg.Provider,
			File:             fc.path,
		}
		run.Tokens.FileSynthesis = append(run.Tokens.FileSynthesis, fileTok)
		run.Tokens.addToTotal(fileTok)

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
		util.Truncate(run.Synthesis.Summary, 800, false))

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

// extractArchitectureGraph uses an LLM to identify architectural components from
// changed files and upserts nodes/edges into the code graph.
func (o *Orchestrator) extractArchitectureGraph(ctx context.Context, run *PipelineRun, owner, repo string) {
	if run.Diff == nil || len(run.Diff.Files) == 0 {
		return
	}

	// Build prompt with file paths + abbreviated diffs
	var prompt strings.Builder
	prompt.WriteString("Changed files:\n")
	for _, f := range run.Diff.Files {
		prompt.WriteString(fmt.Sprintf("\n--- %s ---\n", f.NewName))
		raw := util.Truncate(f.RawDiff, 500, false)
		if len(raw) < len(f.RawDiff) {
			raw += "\n...(truncated)"
		}
		prompt.WriteString(raw)
	}

	// Resolve provider: synthesis first, fallback to review
	lister := storeConfigLister{st: o.st, installationID: run.DBInstallationID}
	provider, cfg, err := o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageSynthesis)
	if err != nil {
		provider, cfg, err = o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageReview)
		if err != nil {
			o.logger.Warn("extractArchitectureGraph: no provider", "error", err)
			return
		}
	}

	graphCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	systemPrompt := `Extract architectural components from code changes for a dependency graph visualization.

Output JSON:
{"nodes": [{"name": "Name", "kind": "module|class|function", "file_path": "path/to/file.ts", "language": "typescript"}], "edges": [{"source": "Name", "target": "DependencyName", "kind": "imports|calls|uses_type|implements"}]}

Rules:
- Prefer concrete classes/structs over interfaces. Only include interfaces if they are central to the architecture (e.g. a core interface that multiple providers implement).
- Use the actual class/struct/module name from the code, not a description.
- "kind" for nodes: module (a file's primary export), class (concrete class/struct), function (standalone function that IS the component)
- "kind" for edges: imports (file-level dependency), calls (runtime invocation), uses_type (type reference), implements (interface implementation)
- Include "calls" edges when you can see function calls between components in the diff — these are the most useful for tracing impact.
- Max 15 nodes per extraction. Quality over quantity.
- file_path must be the exact path from the diff header.`

	req := llm.CompletionRequest{
		Model:  cfg.Model,
		System: systemPrompt,
		Messages: []llm.Message{
			{Role: "user", Content: prompt.String()},
		},
		MaxTokens:   600,
		Temperature: 0.2,
	}

	resp, err := provider.Complete(graphCtx, req)
	if err != nil {
		if graphCtx.Err() != nil {
			// Timeout — retry once with fresh context
			o.logger.Warn("extractArchitectureGraph timeout, retrying", "error", err)
			retryCtx, retryCancel := context.WithTimeout(ctx, 30*time.Second)
			defer retryCancel()
			resp, err = provider.Complete(retryCtx, req)
		}
		if err != nil {
			o.logger.Warn("extractArchitectureGraph LLM failed", "error", err)
			return
		}
	}
	run.Tokens.Graph = StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}
	run.Tokens.addToTotal(run.Tokens.Graph)
	if strings.TrimSpace(resp.Content) == "" {
		o.logger.Warn("extractArchitectureGraph: empty LLM response")
		return
	}

	type graphNode struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		FilePath string `json:"file_path"`
		Language string `json:"language"`
	}
	type graphEdge struct {
		Source string `json:"source"`
		Target string `json:"target"`
		Kind   string `json:"kind"`
	}
	type graphResult struct {
		Nodes []graphNode `json:"nodes"`
		Edges []graphEdge `json:"edges"`
	}

	jsonStr := extractJSON(resp.Content)
	var result graphResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		o.logger.Warn("extractArchitectureGraph parse failed", "error", err, "response_prefix", util.Truncate(resp.Content, 200, true))
		return
	}

	if len(result.Nodes) == 0 {
		return
	}

	// Delete stale nodes for removed files
	for _, f := range run.Diff.Files {
		if f.Status == diff.FileDeleted {
			if err := o.st.DeleteNodesByFile(ctx, run.DBRepoID, f.NewName); err != nil {
				o.logger.Warn("deleteNodesByFile", "error", err, "file", f.NewName)
			}
		}
	}

	// Upsert nodes, collect name→ID
	nodeIDs := make(map[string]int64, len(result.Nodes))
	for _, n := range result.Nodes {
		if n.Name == "" || n.FilePath == "" || n.Kind == "" {
			continue
		}
		id, err := o.st.UpsertCodeNode(ctx, run.DBRepoID, n.Kind, n.Name, n.FilePath, 0, 0, n.Language, run.PREvent.PRNumber)
		if err != nil {
			o.logger.Warn("upsertCodeNode", "error", err, "name", n.Name)
			continue
		}
		nodeIDs[n.Name] = id
	}

	// Upsert edges
	for _, e := range result.Edges {
		srcID, ok1 := nodeIDs[e.Source]
		tgtID, ok2 := nodeIDs[e.Target]
		if !ok1 || !ok2 {
			o.logger.Debug("extractArchitectureGraph: skipping edge, unresolved name", "source", e.Source, "target", e.Target)
			continue
		}
		if err := o.st.UpsertCodeEdge(ctx, run.DBRepoID, srcID, tgtID, e.Kind); err != nil {
			o.logger.Warn("upsertCodeEdge", "error", err, "edge", e.Source+"->"+e.Target)
		}
	}

	o.logger.Info("extracted architecture graph", "nodes", len(nodeIDs), "edges", len(result.Edges), "repo", run.PREvent.RepoFullName)
}

// ─── Lead Agent Helpers ──────────────────────────────────────────────────────

// resolveLeadProvider resolves an LLM provider for lead agent stages,
// trying synthesis first, falling back to review.
func (o *Orchestrator) resolveLeadProvider(ctx context.Context, run *PipelineRun, stage string) (llm.Provider, llm.ModelConfig, bool) {
	lister := storeConfigLister{st: o.st, installationID: run.DBInstallationID}
	provider, cfg, err := o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageSynthesis)
	if err != nil {
		provider, cfg, err = o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageReview)
		if err != nil {
			o.logger.Warn(stage+": no provider", "error", err)
			return nil, llm.ModelConfig{}, false
		}
	}
	return provider, cfg, true
}

// collectAgentResults converts FileReviews into AgentResults keyed by specialist.
func collectAgentResults(reviews []FileReview) []AgentResult {
	var results []AgentResult
	for _, fr := range reviews {
		specialist := "review"
		if len(fr.Comments) > 0 && fr.Comments[0].Specialist != "" {
			specialist = string(fr.Comments[0].Specialist)
		}
		results = append(results, AgentResult{AgentName: specialist, FileReviews: []FileReview{fr}})
	}
	return results
}

// diffFilePaths returns the list of changed file paths from a PatchSet.
func diffFilePaths(d *diff.PatchSet) []string {
	paths := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		paths = append(paths, f.NewName)
	}
	return paths
}

// writeDiffSummary appends truncated diffs for each changed file to a prompt builder.
func writeDiffSummary(sb *strings.Builder, files []diff.FileDiff, maxPerFile int) {
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("\n--- %s ---\n", f.NewName))
		raw := util.Truncate(f.RawDiff, maxPerFile, false)
		if len(raw) < len(f.RawDiff) {
			raw += "\n...(truncated)"
		}
		sb.WriteString(raw)
	}
}

// writeCrossCutting appends cross-cutting concerns from a LeadBrief to a prompt builder.
func writeCrossCutting(sb *strings.Builder, brief *LeadBrief) {
	if brief == nil {
		return
	}
	cc := brief.CrossCuttingConcerns()
	if len(cc) == 0 {
		return
	}
	sb.WriteString("\nCross-cutting concerns from briefing:\n")
	for _, c := range cc {
		sb.WriteString(fmt.Sprintf("- %s\n", c))
	}
}

// groupByFile converts a map of file path -> comments into a slice of FileReviews.
func groupByFile(byFile map[string][]FileComment) []FileReview {
	reviews := make([]FileReview, 0, len(byFile))
	for fp, cs := range byFile {
		reviews = append(reviews, FileReview{Path: fp, Comments: cs})
	}
	return reviews
}

// writeAgentFindings writes detailed per-comment findings to the builder.
func writeAgentFindings(sb *strings.Builder, results []AgentResult) {
	for _, ar := range results {
		sb.WriteString(fmt.Sprintf("\n## %s\n", ar.AgentName))
		for _, fr := range ar.FileReviews {
			for _, c := range fr.Comments {
				body := c.What
				if body == "" {
					body = c.Body
				}
				sb.WriteString(fmt.Sprintf("- %s:%d [%s] %s\n", fr.Path, c.Line, c.Severity, util.Truncate(body, 100, true)))
			}
		}
	}
}

// writeAgentSummary writes a compact per-agent summary (for broadcast — fits in fewer tokens).
func writeAgentSummary(sb *strings.Builder, results []AgentResult) {
	for _, ar := range results {
		var critical, warning, total int
		files := make(map[string]bool)
		var topFindings []string
		for _, fr := range ar.FileReviews {
			files[fr.Path] = true
			for _, c := range fr.Comments {
				total++
				if c.Severity == SeverityCritical {
					critical++
				} else if c.Severity == SeverityWarning {
					warning++
				}
				if len(topFindings) < 3 {
					what := c.What
					if what == "" {
						what = util.Truncate(c.Body, 60, true)
					}
					topFindings = append(topFindings, what)
				}
			}
		}
		if total == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s: %d findings (%d critical, %d warning) across %d files", ar.AgentName, total, critical, warning, len(files)))
		if len(topFindings) > 0 {
			sb.WriteString(": " + strings.Join(topFindings, "; "))
		}
		sb.WriteString("\n")
	}
}

// writeDetailedAgentFindings writes full agent results (comments, simulations, blast impacts).
func writeDetailedAgentFindings(sb *strings.Builder, results []AgentResult) {
	for _, ar := range results {
		sb.WriteString(fmt.Sprintf("\n## %s\n", ar.AgentName))
		for _, fr := range ar.FileReviews {
			for _, c := range fr.Comments {
				body := c.What
				if body == "" {
					body = c.Body
				}
				sb.WriteString(fmt.Sprintf("- file=%s line=%d sev=%s cat=%s: %s\n",
					fr.Path, c.Line, c.Severity, c.Category, util.Truncate(body, 150, true)))
				if c.Why != "" {
					sb.WriteString(fmt.Sprintf("  why: %s\n", util.Truncate(c.Why, 100, true)))
				}
				if c.Suggestion != "" {
					sb.WriteString(fmt.Sprintf("  suggestion: %s\n", util.Truncate(c.Suggestion, 100, true)))
				}
			}
		}
		for _, sim := range ar.SimResults {
			status := "PASS"
			if !sim.Passes {
				status = "FAIL"
			}
			sb.WriteString(fmt.Sprintf("- [simulation %s] %s: %s\n", status, sim.Scenario, util.Truncate(sim.RootCause, 100, true)))
		}
		for _, bi := range ar.BlastImpacts {
			sb.WriteString(fmt.Sprintf("- [blast %s] %s.%s: %s → %s\n",
				bi.Severity, bi.DependentFile, bi.DependentSymbol,
				util.Truncate(bi.AssumptionViolated, 80, true), util.Truncate(bi.FailureMode, 80, true)))
		}
	}
}

// ─── Lead Agent Stage Wrappers ───────────────────────────────────────────────

// leadBriefStage runs the Lead Agent's briefing phase (Phase 1).
func (o *Orchestrator) leadBriefStage(ctx context.Context, run *PipelineRun) error {
	if !run.DeepReview {
		o.logger.Info("[briefing] skipped — deep review not enabled", "pr", run.PREvent.PRNumber)
		return nil
	}
	start := time.Now()
	brief, err := o.leadBrief(ctx, run)
	dur := time.Since(start)
	if err != nil {
		run.LeadAgentError = fmt.Sprintf("leadBriefStage: %s", err)
		o.logger.Warn("[briefing] FAILED", "error", err, "duration_ms", dur.Milliseconds(), "pr", run.PREvent.PRNumber)
		return nil
	}
	if brief == nil {
		o.logger.Warn("[briefing] returned nil — specialists run without brief", "lead_agent_error", run.LeadAgentError, "duration_ms", dur.Milliseconds(), "pr", run.PREvent.PRNumber)
	} else {
		var fileCount int
		for _, item := range brief.Items {
			if item.File != "" {
				fileCount++
			}
		}
		o.logger.Info("[briefing] OK", "files_briefed", fileCount, "cross_cutting", len(brief.CrossCuttingConcerns()), "duration_ms", dur.Milliseconds(), "pr", run.PREvent.PRNumber)
	}
	run.LeadBrief = brief
	return nil
}

// broadcastStage runs the Lead Agent's broadcast phase (Phase 2b + 2c).
// Collects findings, identifies cross-agent signals, runs targeted second passes.
// Runs even without LeadBrief — simulation and blast radius agents are still valuable.
func (o *Orchestrator) broadcastStage(ctx context.Context, run *PipelineRun) error {
	if !run.DeepReview {
		o.logger.Info("[broadcast] skipped — deep review not enabled", "pr", run.PREvent.PRNumber)
		return nil
	}
	start := time.Now()
	o.logger.Info("[broadcast] starting", "findings_in", len(run.FileReviews), "pr", run.PREvent.PRNumber)

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		o.logger.Warn("[broadcast] FAILED — bad repo name", "error", err, "pr", run.PREvent.PRNumber)
		return nil
	}

	allResults := collectAgentResults(run.FileReviews)
	changedPaths := diffFilePaths(run.Diff)

	if simResult := o.runSimulationAgent(ctx, run, changedPaths); simResult != nil {
		o.logger.Info("[broadcast] simulation agent returned", "scenarios", len(simResult.SimResults), "pr", run.PREvent.PRNumber)
		allResults = append(allResults, *simResult)
	} else {
		o.logger.Info("[broadcast] simulation agent: no results (no scenarios or disabled)", "pr", run.PREvent.PRNumber)
	}
	if blastResult := o.runBlastRadiusAgent(ctx, run, owner, repo, changedPaths); blastResult != nil {
		o.logger.Info("[broadcast] blast radius agent returned", "impacts", len(blastResult.BlastImpacts), "pr", run.PREvent.PRNumber)
		allResults = append(allResults, *blastResult)
	} else {
		o.logger.Info("[broadcast] blast radius agent: no results (no graph data or disabled)", "pr", run.PREvent.PRNumber)
	}

	signals := o.leadBroadcast(ctx, run, allResults, run.LeadBrief)
	if len(signals) == 0 {
		o.logger.Info("[broadcast] OK — no cross-agent signals", "duration_ms", time.Since(start).Milliseconds(), "pr", run.PREvent.PRNumber)
		return nil
	}

	o.logger.Info("[broadcast] cross-agent signals found", "signals", len(signals), "pr", run.PREvent.PRNumber)
	for i, sig := range signals {
		o.logger.Info("[broadcast] signal", "i", i, "from", sig.FromAgent, "to", sig.ToAgent, "files", len(sig.FilesToCheck))
	}

	// Phase 2c: Targeted second passes
	for _, sig := range signals {
		fileContents := make(map[string]string)
		for _, path := range sig.FilesToCheck {
			content, fetchErr := o.ghClient.GetFileContent(ctx, run.PREvent.InstallationID, owner, repo, path, run.PREvent.HeadSHA)
			if fetchErr == nil {
				fileContents[path] = truncateLines(content, 200)
			}
		}
		secondPassResults := o.agentSecondPass(ctx, run, sig, fileContents)
		o.logger.Info("[broadcast] second pass complete", "agent", sig.ToAgent, "new_findings", len(secondPassResults))
		run.FileReviews = append(run.FileReviews, secondPassResults...)
	}

	o.logger.Info("[broadcast] OK", "total_signals", len(signals), "findings_out", len(run.FileReviews), "duration_ms", time.Since(start).Milliseconds(), "pr", run.PREvent.PRNumber)
	return nil
}

// runSimulationAgent runs the code simulation sub-agent if enabled.
func (o *Orchestrator) runSimulationAgent(ctx context.Context, run *PipelineRun, changedPaths []string) *AgentResult {
	if !run.CodeSimulation || o.simEngine == nil {
		return nil
	}
	scenarios, err := FindRelevantScenarios(ctx, o.st, run.DBRepoID, changedPaths)
	if err != nil || len(scenarios) == 0 {
		return nil
	}
	simScenarios := make([]SimScenario, len(scenarios))
	for i, s := range scenarios {
		simScenarios[i] = SimScenario{Description: s.Description}
	}
	results, err := o.simEngine.RunSimulations(ctx, SimulationRequest{Run: run, Scenarios: simScenarios})
	if err != nil {
		o.logger.Warn("simulation failed", "error", err)
		return nil
	}
	return &AgentResult{AgentName: "simulation", SimResults: results}
}

// runBlastRadiusAgent runs the blast radius sub-agent if enabled.
func (o *Orchestrator) runBlastRadiusAgent(ctx context.Context, run *PipelineRun, owner, repo string, changedPaths []string) *AgentResult {
	if !run.BlastRadius || o.st == nil {
		return nil
	}
	changedSet := make(map[string]bool, len(changedPaths))
	for _, p := range changedPaths {
		changedSet[p] = true
	}
	nodes, err := o.st.GetBlastRadius(ctx, run.DBRepoID, changedPaths, 2)
	if err != nil || len(nodes) == 0 {
		return nil
	}
	// Fetch up to 3 dependent file contents (depth-1 only, excluding changed files)
	depContents := make(map[string]string)
	seen := make(map[string]bool)
	for _, n := range nodes {
		if n.Depth != 1 || changedSet[n.FilePath] || seen[n.FilePath] || len(depContents) >= 3 {
			continue
		}
		seen[n.FilePath] = true
		content, fetchErr := o.ghClient.GetFileContent(ctx, run.PREvent.InstallationID, owner, repo, n.FilePath, run.PREvent.HeadSHA)
		if fetchErr == nil {
			depContents[n.FilePath] = truncateLines(content, 200)
		}
	}
	if len(depContents) == 0 {
		return nil
	}
	impacts := o.analyzeBlastRadius(ctx, run, owner, repo, depContents)
	if len(impacts) == 0 {
		return nil
	}
	return &AgentResult{AgentName: "blast_radius", BlastImpacts: impacts}
}

func (o *Orchestrator) dedupStage(ctx context.Context, run *PipelineRun) error {
	if !run.DeepReview {
		return nil
	}
	before := countComments(run)
	run.AllFileReviews = make([]FileReview, len(run.FileReviews))
	copy(run.AllFileReviews, run.FileReviews)

	run.FileReviews = dedupFindings(run.FileReviews, 5)
	after := countComments(run)
	o.logger.Info("[dedup] OK", "before", before, "after", after, "removed", before-after, "pr", run.PREvent.PRNumber)
	return nil
}

// validateStage enriches deduplicated findings with blast radius and simulation data.
// Runs blast radius + simulation in parallel on the clean finding set.
func (o *Orchestrator) validateStage(ctx context.Context, run *PipelineRun) error {
	if !run.DeepReview {
		o.logger.Info("[validate] skipped — deep review not enabled", "pr", run.PREvent.PRNumber)
		return nil
	}
	start := time.Now()
	o.logger.Info("[validate] starting", "findings", countComments(run), "pr", run.PREvent.PRNumber)

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		o.logger.Warn("[validate] bad repo name, skipping", "error", err, "pr", run.PREvent.PRNumber)
		return nil
	}

	changedPaths := diffFilePaths(run.Diff)

	// Run blast radius and simulation in parallel
	var blastImpacts []BlastRadiusImpact
	var simResults []SimulationResult
	var wg sync.WaitGroup

	// Blast Radius
	wg.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				o.logger.Error("[validate] blast radius goroutine panic", "recover", r, "pr", run.PREvent.PRNumber)
			}
		}()
		defer wg.Done()
		if !run.BlastRadius || o.st == nil {
			return
		}
		changedSet := make(map[string]bool, len(changedPaths))
		for _, p := range changedPaths {
			changedSet[p] = true
		}
		nodes, err := o.st.GetBlastRadius(ctx, run.DBRepoID, changedPaths, 2)
		if err != nil {
			o.logger.Warn("[validate] blast radius query failed", "error", err, "pr", run.PREvent.PRNumber)
			return
		}
		if len(nodes) == 0 {
			return
		}
		depContents := make(map[string]string)
		seen := make(map[string]bool)
		for _, n := range nodes {
			if n.Depth != 1 || changedSet[n.FilePath] || seen[n.FilePath] || len(depContents) >= 3 {
				continue
			}
			seen[n.FilePath] = true
			content, fetchErr := o.ghClient.GetFileContent(ctx, run.PREvent.InstallationID, owner, repo, n.FilePath, run.PREvent.HeadSHA)
			if fetchErr == nil {
				depContents[n.FilePath] = truncateLines(content, 200)
			}
		}
		if len(depContents) > 0 {
			blastImpacts = o.analyzeBlastRadius(ctx, run, owner, repo, depContents)
		}
		o.logger.Info("[validate] blast radius", "impacts", len(blastImpacts), "dependents_checked", len(depContents), "pr", run.PREvent.PRNumber)
	}()

	// Simulation
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				o.logger.Error("[validate] simulation goroutine panic", "recover", r, "pr", run.PREvent.PRNumber)
			}
		}()
		if !run.CodeSimulation || o.simEngine == nil {
			return
		}
		scenarios, err := FindRelevantScenarios(ctx, o.st, run.DBRepoID, changedPaths)
		if err != nil {
			o.logger.Warn("[validate] scenario lookup failed", "error", err, "pr", run.PREvent.PRNumber)
			return
		}
		if len(scenarios) == 0 {
			return
		}
		simScenarios := make([]SimScenario, len(scenarios))
		for i, s := range scenarios {
			simScenarios[i] = SimScenario{Description: s.Description, Severity: s.Severity, Files: s.Files}
		}
		req := SimulationRequest{Run: run, Scenarios: simScenarios}
		results, simErr := o.simEngine.RunSimulations(ctx, req)
		if simErr != nil {
			o.logger.Warn("[validate] simulation failed", "error", simErr, "pr", run.PREvent.PRNumber)
		} else {
			simResults = results
		}
		o.logger.Info("[validate] simulation", "scenarios_tested", len(simScenarios), "results", len(simResults), "pr", run.PREvent.PRNumber)
	}()

	wg.Wait()

	// Store results on PipelineRun for scoring and synthesis to use
	if run.Synthesis == nil {
		run.Synthesis = &SynthesisResult{}
	}
	run.Synthesis.SimulationResults = simResults

	// Annotate findings with blast radius data
	// Count total dependents for each changed file (not the dependent files themselves)
	if len(blastImpacts) > 0 {
		// All changed files have blast radius impact — count total downstream breakage
		totalImpacts := len(blastImpacts)
		changedSet := make(map[string]bool, len(changedPaths))
		for _, p := range changedPaths {
			changedSet[p] = true
		}
		for i := range run.FileReviews {
			fr := &run.FileReviews[i]
			if changedSet[fr.Path] {
				for j := range fr.Comments {
					fr.Comments[j].BlastRadius = totalImpacts
				}
			}
		}
	}

	o.logger.Info("[validate] OK", "blast_impacts", len(blastImpacts), "sim_results", len(simResults), "duration_ms", time.Since(start).Milliseconds(), "pr", run.PREvent.PRNumber)
	return nil
}

// ─── Lead Agent Functions ────────────────────────────────────────────────────

const leadBriefSystemPrompt = `You coordinate 4 specialist reviewers: Bug Hunter, Security Auditor, Architecture Reviewer, Regression Reviewer.

Read the PR and produce a briefing as a JSON array. Each element is either a file brief or a cross-cutting concern.

File brief: {"file": "path", "summary": "what changed", "bug": "focus for bug hunter", "security": "focus for security", "arch": "focus for architecture", "regression": "focus for regression"}
Cross-cutting: {"cross_cutting": "concern spanning multiple files"}

Keep each focus field to 1 sentence. Output JSON array only.

Example:
[{"file": "src/auth.ts", "summary": "Session management with token refresh", "bug": "Token expiry edge cases, race in concurrent refresh", "security": "Session fixation, CSRF, token storage", "arch": "Error propagation from refresh to callers", "regression": "Return type change affects authenticated endpoints"}, {"cross_cutting": "auth.ts and api.ts share a global session cache"}]`

// leadBrief produces focus areas for each specialist by reading the whole PR.
// Non-fatal: returns nil on error so specialists run without briefs.
func (o *Orchestrator) leadBrief(ctx context.Context, run *PipelineRun) (*LeadBrief, error) {
	if run.Diff == nil || len(run.Diff.Files) == 0 {
		return nil, nil
	}

	provider, cfg, ok := o.resolveLeadProvider(ctx, run, "leadBrief")
	if !ok {
		run.LeadAgentError = "leadBrief: no LLM provider available"
		return nil, nil
	}

	var prompt strings.Builder
	safeTitle := sanitizeUserInput(util.Truncate(run.PREvent.PRTitle, 200, false))
	safeAuthor := sanitizeUserInput(util.Truncate(run.PREvent.PRAuthor, 100, false))
	prompt.WriteString(fmt.Sprintf("PR #%d: \"%s\" by %s\n\nChanged files:\n", run.PREvent.PRNumber, safeTitle, safeAuthor))
	writeDiffSummary(&prompt, run.Diff.Files, 500)

	briefCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	resp, err := provider.Complete(briefCtx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      leadBriefSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   1500,
		Temperature: 0.2,
	})
	if err != nil {
		run.LeadAgentError = fmt.Sprintf("leadBrief LLM failed: %s", err)
		o.logger.Warn("leadBrief LLM failed", "error", err)
		return nil, nil
	}

	items, err := unmarshalLLMArray[BriefItem](resp.Content)
	if err != nil {
		run.LeadAgentError = fmt.Sprintf("leadBrief parse failed: %s | response: %s", err, util.Truncate(resp.Content, 300, true))
		o.logger.Warn("leadBrief parse failed", "error", err, "response_prefix", util.Truncate(resp.Content, 200, true))
		return nil, nil
	}
	if len(items) == 0 {
		run.LeadAgentError = "leadBrief: LLM returned empty array"
		o.logger.Warn("leadBrief returned empty array")
		return nil, nil
	}

	brief := &LeadBrief{Items: items}
	var fileCount int
	for _, item := range items {
		if item.File != "" {
			fileCount++
		}
	}
	o.logger.Info("lead brief produced", "files", fileCount, "cross_cutting", len(brief.CrossCuttingConcerns()))
	return brief, nil
}

const leadBroadcastSystemPrompt = `You have findings from multiple specialist agents reviewing a PR. Identify cross-agent signals — cases where one agent's finding should trigger another agent to re-examine specific code.

Only flag signals where a second pass would likely find something NEW that the first pass missed.

Examples:
- Security found auth bypass → Bug Hunter should check callers
- Simulation predicts scenario breaks → Architecture should verify error chain
- Blast Radius shows dependent breaks → Regression should verify caller handling

Return [] if no cross-agent signals needed.

Output JSON array:
[{"from_agent": "security", "to_agent": "bug_hunter", "signal": "Auth bypass in session.validate()", "question": "Do callers handle false positive auth?", "files_to_check": ["src/api/handler.ts"]}]`

// leadBroadcast identifies cross-agent signals after all agents finish.
// Non-fatal: returns empty slice on error so second pass is skipped.
func (o *Orchestrator) leadBroadcast(ctx context.Context, run *PipelineRun, allResults []AgentResult, brief *LeadBrief) []CrossAgentSignal {
	provider, cfg, ok := o.resolveLeadProvider(ctx, run, "leadBroadcast")
	if !ok {
		return nil
	}

	var prompt strings.Builder
	prompt.WriteString("Agent findings summary:\n")
	writeAgentSummary(&prompt, allResults)
	writeCrossCutting(&prompt, brief)

	broadcastCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	resp, err := provider.Complete(broadcastCtx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      leadBroadcastSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   800,
		Temperature: 0.2,
	})
	if err != nil {
		o.logger.Warn("leadBroadcast LLM failed", "error", err)
		return nil
	}

	signals, err := unmarshalLLMArray[CrossAgentSignal](resp.Content)
	if err != nil {
		o.logger.Warn("leadBroadcast parse failed", "error", err, "response_prefix", util.Truncate(resp.Content, 200, true))
		return nil
	}

	o.logger.Info("lead broadcast signals", "count", len(signals))
	return signals
}

// agentSecondPass re-examines files based on a cross-agent signal.
// Non-fatal: returns empty slice on error.
func (o *Orchestrator) agentSecondPass(ctx context.Context, run *PipelineRun, signal CrossAgentSignal, fileContents map[string]string) []FileReview {
	lister := storeConfigLister{st: o.st, installationID: run.DBInstallationID}
	provider, cfg, err := o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageReview)
	if err != nil {
		o.logger.Warn("agentSecondPass: no provider", "error", err)
		return nil
	}

	systemPrompt := specialistPrompt(Specialist(signal.ToAgent), run.Prompts)

	var prompt strings.Builder
	prompt.WriteString(fmt.Sprintf("Cross-agent signal from %s:\n", signal.FromAgent))
	prompt.WriteString(fmt.Sprintf("Signal: %s\n", signal.Signal))
	prompt.WriteString(fmt.Sprintf("Question: %s\n\n", signal.Question))
	prompt.WriteString("Files to re-examine:\n")
	for _, fp := range signal.FilesToCheck {
		if content, ok := fileContents[fp]; ok {
			prompt.WriteString(fmt.Sprintf("\n--- %s ---\n%s\n", fp, util.Truncate(content, 2000, false)))
		}
	}

	passCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	resp, err := provider.Complete(passCtx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      systemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   600,
		Temperature: 0.3,
	})
	if err != nil {
		o.logger.Warn("agentSecondPass LLM failed", "error", err, "agent", signal.ToAgent)
		return nil
	}

	type secondPassComment struct {
		FilePath   string   `json:"file_path"`
		Severity   Severity `json:"severity"`
		Category   Category `json:"category"`
		Line       int      `json:"line"`
		What       string   `json:"what"`
		Why        string   `json:"why"`
		Suggestion string   `json:"suggestion"`
	}
	parsed, err := unmarshalLLMArray[secondPassComment](resp.Content)
	if err != nil {
		o.logger.Warn("agentSecondPass parse failed", "error", err, "agent", signal.ToAgent)
		return nil
	}

	// Group comments by file path; fallback to first file if LLM omits file_path
	byFile := make(map[string][]FileComment)
	for _, c := range parsed {
		fp := c.FilePath
		if fp == "" && len(signal.FilesToCheck) > 0 {
			fp = signal.FilesToCheck[0]
		}
		byFile[fp] = append(byFile[fp], FileComment{
			Severity:   c.Severity,
			Category:   c.Category,
			Line:       c.Line,
			What:       c.What,
			Why:        c.Why,
			Suggestion: c.Suggestion,
			Specialist: Specialist(signal.ToAgent + "_second_pass"),
		})
	}
	return groupByFile(byFile)
}

const blastRadiusAgentPrompt = `You analyze dependency impact. Given changed code and dependent file source, identify concrete breaking changes.

For each dependent:
1. What does it assume about the changed code? (return type, error behavior, side effects)
2. Do the changes violate those assumptions?
3. What's the concrete failure mode?

Only report with evidence from BOTH changed code AND dependent code.

Output JSON array:
[{"dependent_file": "...", "dependent_symbol": "...", "assumption_violated": "...", "failure_mode": "...", "severity": "critical|warning"}]
Return [] if nothing breaks.`

// analyzeBlastRadius checks if dependent code breaks due to PR changes.
// Non-fatal: returns nil on error.
func (o *Orchestrator) analyzeBlastRadius(ctx context.Context, run *PipelineRun, owner, repo string, depContents map[string]string) []BlastRadiusImpact {
	if run.Diff == nil || len(run.Diff.Files) == 0 || len(depContents) == 0 {
		return nil
	}

	provider, cfg, ok := o.resolveLeadProvider(ctx, run, "analyzeBlastRadius")
	if !ok {
		return nil
	}

	var prompt strings.Builder
	prompt.WriteString("Changed files:\n")
	writeDiffSummary(&prompt, run.Diff.Files, 500)

	prompt.WriteString("\n\nDependent files:\n")
	for fp, content := range depContents {
		prompt.WriteString(fmt.Sprintf("\n--- %s ---\n%s\n", fp, util.Truncate(content, 1500, false)))
	}

	blastCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	resp, err := provider.Complete(blastCtx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      blastRadiusAgentPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   600,
		Temperature: 0.2,
	})
	if err != nil {
		o.logger.Warn("analyzeBlastRadius LLM failed", "error", err)
		return nil
	}

	impacts, err := unmarshalLLMArray[BlastRadiusImpact](resp.Content)
	if err != nil {
		o.logger.Warn("analyzeBlastRadius parse failed", "error", err, "response_prefix", util.Truncate(resp.Content, 200, true))
		return nil
	}

	o.logger.Info("blast radius analysis", "impacts", len(impacts), "repo", fmt.Sprintf("%s/%s", owner, repo))
	return impacts
}

const leadCrossCheckSystemPrompt = `You are the lead reviewer finalizing your team's findings.

Tasks:
1. DEDUPLICATE: Keep the best explanation for each unique issue. Remove duplicates.
2. CROSS-REFERENCE: Connect related findings across files into unified findings.
3. GAP CHECK: Were all cross-cutting concerns from the brief addressed? Flag unaddressed ones.
4. SEVERITY: Ensure consistent severity. Same class of bug = same severity.
5. QUALITY FILTER: Remove speculative findings, vague suggestions, linter-catchable issues.
6. INTEGRATE: Merge simulation failures and blast radius impacts with specialist findings.

Output: Final JSON array of comments, same format as specialist output.
Each comment must have: severity, category, line, what, why, suggestion (optional), file_path.`

// leadCrossCheck synthesizes all findings from agents, deduplicates, and produces
// the final set of review comments. Replaces the scoring stage.
func (o *Orchestrator) leadCrossCheck(ctx context.Context, run *PipelineRun, allResults []AgentResult, brief *LeadBrief) []FileReview {
	provider, cfg, ok := o.resolveLeadProvider(ctx, run, "leadCrossCheck")
	if !ok {
		return nil
	}

	var prompt strings.Builder
	prompt.WriteString("All agent findings:\n")
	writeDetailedAgentFindings(&prompt, allResults)
	writeCrossCutting(&prompt, brief)

	crossCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	resp, err := provider.Complete(crossCtx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      leadCrossCheckSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt.String()}},
		MaxTokens:   2000,
		Temperature: 0.3,
	})
	if err != nil {
		o.logger.Warn("leadCrossCheck LLM failed", "error", err)
		return nil
	}

	type crossCheckComment struct {
		FilePath   string   `json:"file_path"`
		Line       int      `json:"line"`
		Severity   Severity `json:"severity"`
		Category   Category `json:"category"`
		What       string   `json:"what"`
		Why        string   `json:"why"`
		Suggestion string   `json:"suggestion,omitempty"`
	}

	comments, err := unmarshalLLMArray[crossCheckComment](resp.Content)
	if err != nil {
		o.logger.Warn("leadCrossCheck parse failed", "error", err, "response_prefix", util.Truncate(resp.Content, 200, true))
		return nil
	}

	byFile := make(map[string][]FileComment)
	for _, c := range comments {
		fc := FileComment{
			Line:       c.Line,
			Severity:   c.Severity,
			Category:   c.Category,
			What:       c.What,
			Why:        c.Why,
			Suggestion: c.Suggestion,
		}
		if !ValidSeverities[fc.Severity] {
			fc.Severity = SeverityWarning
		}
		if !ValidCategories[fc.Category] {
			fc.Category = CategoryBug
		}
		byFile[c.FilePath] = append(byFile[c.FilePath], fc)
	}

	reviews := groupByFile(byFile)
	o.logger.Info("lead cross-check complete", "files", len(reviews), "comments", len(comments))
	return reviews
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
	provider, cfg, err := o.reviewStage.registry.ResolveProvider(ctx, storeConfigLister{st: o.st, installationID: run.DBInstallationID}, run.DBInstallationID, run.DBRepoID, llm.StageReview)
	if err != nil {
		return llm.ModelConfig{}, nil, err
	}
	return cfg, provider, nil
}


func getDiffContext(run *PipelineRun, path string) string {
	if run.Diff == nil {
		return ""
	}
	for _, f := range run.Diff.Files {
		if f.NewName == path {
			return util.Truncate(f.RawDiff, 1000, false)
		}
	}
	return ""
}

// formatCommentBody builds the GitHub review comment body with severity, category, and optional suggestion block.
func formatCommentBody(c FileComment) string {
	title := fmt.Sprintf("**[%s · %s]** %s", c.Severity, c.Category, commentTitle(c))

	var body string
	if c.What != "" && c.Why != "" {
		body = title + "\n\n**What:** " + c.What + "\n\n**Why:** " + c.Why
	} else {
		body = title + "\n\n" + c.Body
	}

	if c.Suggestion != "" {
		body += "\n\n```suggestion\n" + strings.TrimRight(c.Suggestion, "\n") + "\n```"
	}

	// Feedback prompt — Argus auto-learns, devs can dismiss
	if c.Severity == SeverityCritical || c.Severity == SeverityWarning {
		body += "\n\n---\n<sub>Argus learns from this automatically · React 👎 to dismiss</sub>"
	}

	return body
}

// commentTitle extracts a short title from the comment for the header line.
func commentTitle(c FileComment) string {
	src := c.What
	if src == "" {
		src = c.Body
	}
	if idx := strings.Index(src, "."); idx > 0 && idx < 80 {
		return src[:idx]
	}
	return util.Truncate(src, 80, false)
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
	var criticals, warnings, suggestions int
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			switch c.Severity {
			case SeverityCritical:
				criticals++
			case SeverityWarning:
				warnings++
			case SeveritySuggestion:
				suggestions++
			}
		}
	}
	if criticals+warnings+suggestions == 0 {
		return 10
	}
	// Log-scaled penalty: diminishing impact per additional finding
	// 1 critical = 2.0, 5 criticals = 3.2, 10 criticals = 3.6
	penalty := 0.0
	if criticals > 0 {
		penalty += math.Log2(float64(criticals)+1) * 1.5
	}
	if warnings > 0 {
		penalty += math.Log2(float64(warnings)+1) * 0.7
	}
	if suggestions > 0 {
		penalty += math.Log2(float64(suggestions)+1) * 0.2
	}
	score := int(math.Round(10 - penalty))
	return max(1, min(10, score))
}

// RecoverIncomplete resumes any in-flight pipeline runs after a restart.
func (o *Orchestrator) RecoverIncomplete(ctx context.Context) error {
	return o.sm.RecoverIncomplete(ctx)
}
