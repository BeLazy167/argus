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
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BeLazy167/argus/backend/internal/config"
	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/graph"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/obs"
	"github.com/BeLazy167/argus/backend/internal/sast"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/util"
	"github.com/BeLazy167/argus/backend/pkg/diff"
	gh "github.com/google/go-github/v68/github"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxTokens budgets for LLM calls across the pipeline. Kept together so the
// next model-tuning pass touches one file, not ten. (Lead-agent coordination
// budgets live beside their sole callers in coordination.go.)
//
// gpt-5.x reasoning tokens count against max_completion_tokens, so these are
// sized with headroom for invisible reasoning ON TOP of the visible JSON
// output: 4000 is the baseline (2000 output + 2000 reasoning).
const (
	// Coordination validators — intent extract, issue acceptance, cross-PR.
	intentMaxTokens     = 4000
	acceptanceMaxTokens = 4000
	crossPRMaxTokens    = 4000

	// Post-pipeline memory synthesis (one call per hot file).
	fileSynthesisMaxTokens = 4000

	// Synthesis brief posted to the PR (Headline + Verdict + top-priority +
	// fix-order + architecture lines, plus reasoning tokens).
	synthesisBriefMaxTokens = 4000
)

// splitRepoFullName splits "owner/repo" into its components.
func splitRepoFullName(fullName string) (owner, repo string, err error) {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repo name: %s", fullName)
	}
	return parts[0], parts[1], nil
}

// matchesSkipBranches returns true if branch matches any of the glob patterns.
func matchesSkipBranches(branch string, patterns []string) bool {
	for _, p := range patterns {
		matched, err := filepath.Match(p, branch)
		if err != nil {
			slog.Warn("invalid skip_base_branches pattern", "pattern", p, "error", err)
			continue
		}
		if matched {
			return true
		}
	}
	return false
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

// fetchPRDiff fetches and parses a PR's unified diff, falling back to the
// per-file API when GitHub returns 406 (diff too large). Shared by
// HandlePREvent and the no-run retry path so both construct the diff the same
// way.
func (o *Orchestrator) fetchPRDiff(ctx context.Context, event *ghpkg.PREvent, owner, repo string) (*diff.PatchSet, string, error) {
	rawDiff, err := o.ghClient.GetPRDiff(ctx, event.InstallationID, owner, repo, event.PRNumber)
	if err != nil && isDiffTooLarge(err) {
		o.logger.Warn("diff too large, falling back to files API", "pr", event.PRNumber, "error", err)
		patchSet, rawDiff, ferr := o.fetchDiffViaFiles(ctx, event, owner, repo)
		if ferr != nil {
			return nil, "", fmt.Errorf("fallback files API: %w", ferr)
		}
		return patchSet, rawDiff, nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("fetching diff: %w", err)
	}
	patchSet, err := diff.Parse(rawDiff)
	if err != nil {
		return nil, "", fmt.Errorf("parsing diff: %w", err)
	}
	return patchSet, rawDiff, nil
}

// buildRunInput carries the per-review inputs buildRun needs. Grouped into a
// struct because they come from several sources (PR event, fetched diff,
// resolved installation/repo ids, incremental context) — a positional
// signature with this many args is error-prone.
type buildRunInput struct {
	reviewID         uuid.UUID
	event            ghpkg.PREvent
	patchSet         *diff.PatchSet
	rawDiff          string
	dbInstallationID int64
	dbRepoID         int64
	traceID          string
	isIncremental    bool
	previousReviewID *uuid.UUID
	indexer          memory.Indexer
}

// buildRun constructs a fresh PipelineRun from a PR event, its fetched diff, and
// the repo's merged settings + prompts, then computes the review contract.
// Shared by HandlePREvent (new reviews) and the no-run retry path (existing
// review id) so feature-flag resolution and contract computation live in one
// place.
func (o *Orchestrator) buildRun(ctx context.Context, in buildRunInput) *PipelineRun {
	// Merge org defaults with repo overrides (repo wins)
	mergedSettings, mergedErr := o.st.GetMergedSettings(ctx, in.dbInstallationID, in.dbRepoID)
	if mergedErr != nil {
		o.logger.Error("failed to load merged settings, using defaults", "error", mergedErr, "installation", in.dbInstallationID, "repo", in.dbRepoID)
	}

	run := &PipelineRun{
		ID:                  uuid.New(),
		ReviewID:            in.reviewID,
		State:               StatePending,
		PREvent:             in.event,
		DBInstallationID:    in.dbInstallationID,
		DBRepoID:            in.dbRepoID,
		TraceID:             in.traceID,
		Diff:                in.patchSet,
		RawDiff:             in.rawDiff,
		Persona:             loadPersona(mergedSettings),
		CustomPersonaPrompt: loadCustomPersonaPrompt(mergedSettings),
		DeepReview: isDeepReviewEnabled(mergedSettings) && func() bool {
			tier, _ := o.st.GetPlanTier(ctx, in.dbInstallationID)
			return o.cfg.IsPro(tier)
		}(),
		CrossFileContext:  isCrossFileContextEnabled(mergedSettings),
		BlastRadius:       isBlastRadiusEnabled(mergedSettings),
		ScenarioMemory:    isScenarioMemoryEnabled(mergedSettings),
		CodeSimulation:    isCodeSimulationEnabled(mergedSettings),
		PREnrichment:      isPREnrichmentEnabled(mergedSettings),
		LearnPatterns:     isLearnPatternsEnabled(mergedSettings),
		LearnConventions:  isLearnConventionsEnabled(mergedSettings),
		FileSynthesis:     isFileSynthesisEnabled(mergedSettings),
		ArchitectureGraph: isArchitectureGraphEnabled(mergedSettings),
		Prompts:           o.loadPrompts(ctx, in.dbRepoID),
		IsIncremental:     in.isIncremental,
		PreviousReviewID:  in.previousReviewID,
		Indexer:           in.indexer,
		Thresholds:        parseThresholds(mergedSettings),
		EventBus:          o.eventBus,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	run.Contract = ComputeContract(&run.PREvent, in.patchSet.Files)
	return run
}

// resolveIndexer returns the per-org memory indexer, or nil when memory is not
// configured for the installation.
func (o *Orchestrator) resolveIndexer(ctx context.Context, dbInstallationID int64) memory.Indexer {
	if o.memRegistry == nil {
		return nil
	}
	return o.memRegistry.GetIndexer(ctx, dbInstallationID)
}

// buildPriorComments maps persisted review comments into the per-file
// PriorComment map the review stage uses to dedup against / verify prior
// findings on an incremental (or no-run retry) re-review. Returns nil for an
// empty input. Pure — shared by HandlePREvent and retryFromReviewRow.
func buildPriorComments(comments []store.ReviewComment) map[string][]PriorComment {
	if len(comments) == 0 {
		return nil
	}
	out := make(map[string][]PriorComment)
	for _, c := range comments {
		// DB stores StartLine (range start) and EndLine (range end / single line).
		// Map directly: Line = start of range, EndLine = end of range.
		line, endLine := 0, 0
		if c.StartLine != nil {
			line = *c.StartLine
		}
		if c.EndLine != nil {
			endLine = *c.EndLine
		}
		// If only EndLine is set (single-line comment), use it as both.
		if line == 0 && endLine > 0 {
			line = endLine
		}
		sev := "suggestion"
		if c.Severity != nil {
			sev = *c.Severity
		}
		cat := ""
		if c.Category != nil {
			cat = *c.Category
		}
		out[c.FilePath] = append(out[c.FilePath], PriorComment{
			FilePath: c.FilePath,
			Line:     line,
			EndLine:  endLine,
			Body:     c.Body,
			Severity: sev,
			Category: cat,
		})
	}
	return out
}

// Orchestrator receives PR events and drives them through the review pipeline.
type Orchestrator struct {
	db           *pgxpool.Pool
	st           *store.Store
	ghClient     *ghpkg.Client
	sm           *StateMachine
	reviewStage  *ReviewStage
	triageStage  *TriageStage
	intentStage  *IntentExtractionStage // optional; nil disables intent extraction
	scoringStage *ScoringStage
	memRegistry  *memory.Registry
	simEngine    *SimulationEngine
	registry     LLMRegistry
	eventBus     *EventBus
	logger       *slog.Logger
	cfg          *config.Config
	// lifecycle is the single home for the review-lifecycle DB guards
	// (EnsureNotRunning / CancelStranded / ShouldAbortPost). See lifecycle.go.
	lifecycle *ReviewLifecycle
	// incremental resolves the one re-review decision (is-incremental + priors +
	// inter-diff) shared by HandlePREvent, the retry paths, and auto-resolve, so
	// the inter-diff GitHub round-trip fires once per push. See incremental.go.
	incremental *IncrementalResolver
	// crossPRHooks is the test-only injection point for the async cross-PR
	// stage. nil in production — the stage falls back to the concrete
	// st/ghClient/sm fields. Tests assign a non-nil *crossPRHooks to swap
	// those boundaries with in-memory fakes (see crosspr_stage_deps.go and
	// crosspr_stage_integration_test.go).
	crossPRHooks *crossPRHooks
	// enrichStoreOverride is the test-only PatternLinker seam for the Enricher.
	// nil in production — enrichStoreDep() falls back to a default adapter over
	// o.st. Tests assign an in-memory fake to exercise the DB-less enrich path
	// (see enrich_deps.go and enrich_read_test.go).
	enrichStoreOverride PatternLinker
}

// LLMRegistry is the subset of llm.Registry used by Orchestrator.
type LLMRegistry interface {
	HasKeyForRepo(ctx context.Context, installationID int64, repoID *int64, providerName string) bool
}

func NewOrchestrator(db *pgxpool.Pool, st *store.Store, ghClient *ghpkg.Client, reviewStage *ReviewStage, triageStage *TriageStage, intentStage *IntentExtractionStage, scoringStage *ScoringStage, memRegistry *memory.Registry, registry LLMRegistry, eventBus *EventBus, logger *slog.Logger, cfg *config.Config) *Orchestrator {
	sm := NewStateMachine(db, st, logger)
	sm.eventBus = eventBus

	o := &Orchestrator{
		db:           db,
		st:           st,
		ghClient:     ghClient,
		sm:           sm,
		reviewStage:  reviewStage,
		triageStage:  triageStage,
		intentStage:  intentStage,
		scoringStage: scoringStage,
		memRegistry:  memRegistry,
		registry:     registry,
		eventBus:     eventBus,
		logger:       logger,
		cfg:          cfg,
	}
	o.lifecycle = NewReviewLifecycle(db, st, sm, eventBus, logger)
	o.incremental = NewIncrementalResolver(st, ghClient, logger)
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

	// Async cross-PR stage: runs when any review completes. See
	// crosspr_stage.go:OnReviewCompleted. Subscription is global (bus-level)
	// because the per-review SSE topic is torn down once the UI disconnects;
	// EventReviewCompleted is a lifecycle signal, not a UI stream.
	//
	// The handler spawns a goroutine so it never blocks Publish. Panic
	// recovery mirrors the same defer-recover pattern used by the other
	// async handlers spawned from this constructor.
	if eventBus != nil {
		eventBus.SubscribeGlobal(func(reviewID uuid.UUID, evt Event) {
			if evt.Type != EventReviewCompleted {
				return
			}
			var p ReviewCompletedPayload
			if err := json.Unmarshal(evt.Data, &p); err != nil {
				logger.Error("EventReviewCompleted bad payload",
					"review_id", reviewID, "error", err)
				return
			}
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("crosspr-stage dispatch panic",
							"recover", r,
							"review_id", p.ReviewID,
							"stack", string(debug.Stack()))
						emitPipelinePanicEvent(context.Background(), logger, "crosspr_dispatch", r, "")
					}
				}()
				// Detach from request context at the call site — the bus
				// publisher's ctx is not meaningful for a long-running
				// async stage. OnReviewCompleted further detaches internally.
				o.OnReviewCompleted(context.Background(), p.ReviewID)
			}()
			// Joint acceptance runs in parallel to the findings
			// stage. Separate mutex + no debounce — findings stage owns
			// the 30s debounce, joint acceptance piggybacks on it by
			// virtue of running from the same event; if multiple events
			// fire for a review, the per-review mutex in
			// runCrossPRAcceptanceStage serializes work.
			go func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Error("joint-accept dispatch panic",
							"recover", r,
							"review_id", p.ReviewID,
							"stack", string(debug.Stack()))
						emitPipelinePanicEvent(context.Background(), logger, "joint_accept_dispatch", r, "")
					}
				}()
				o.runCrossPRAcceptanceStage(context.Background(), p.ReviewID)
			}()
		})
	}

	// Start the cross-PR map sweeper so per-reviewID mutexes and
	// per-key sliding-window counters don't accumulate over process
	// lifetime. See startCrossPRSweeper for interval + maxAge.
	o.startCrossPRSweeper(context.Background())

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
	// Attribute every slog event fired downstream to this installation + PR
	// author so the PostHog handler's fallback chain resolves a real
	// distinct_id instead of the droppedUnattributed bucket. Must run BEFORE
	// any subsequent log call (including PipelineRun construction) in this
	// handler's stack so the whole trace carries attribution.
	ctx = obs.SetInstallationID(ctx, inst.ID)
	if event.PRAuthor != "" {
		ctx = obs.SetGithubLogin(ctx, event.PRAuthor)
	}
	dbRepo, err := o.st.UpsertRepo(ctx, inst.ID, event.RepoID, event.RepoFullName, event.BaseRef)
	if err != nil {
		return fmt.Errorf("upserting repo: %w", err)
	}

	if !dbRepo.Enabled {
		o.logger.Info("skipping disabled repo", "repo", event.RepoFullName)
		return nil
	}

	// Branch filtering: skip if base branch matches skip_base_branches patterns
	if settings, ok := parseRepoSettings(dbRepo.SettingsJSON); ok && len(settings.SkipBaseBranches) > 0 {
		if matchesSkipBranches(event.BaseRef, settings.SkipBaseBranches) {
			o.logger.Info("skipping review: base branch filtered", "repo", event.RepoFullName, "base", event.BaseRef)
			return nil
		}
	}

	// Check if repo has a model config + API key for the review stage.
	// Use the fallback-aware query so that org-level model configs (repo_id=NULL)
	// satisfy the readiness gate when per-repo overrides aren't set. Without the
	// fallback, repos relying on org defaults fail the gate and re-post the
	// "welcome to Argus" comment on every PR.
	var dbConfigs []store.ModelConfig
	if o.registry != nil {
		dbConfigs, err = o.st.ListModelConfigsWithFallback(ctx, inst.ID, dbRepo.ID)
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
			// Suppress duplicate welcome comments on the same PR. Users retrying
			// `@argus-eye review` while the config is still missing shouldn't
			// generate a stack of identical onboarding comments.
			alreadyWelcomed, hwErr := o.st.HasFailedReviewWithError(ctx, dbRepo.ID, event.PRNumber, "no_api_key")
			if hwErr != nil {
				o.logger.Error("checking prior no_api_key failure", "error", hwErr, "repo", event.RepoFullName)
			}
			if !alreadyWelcomed {
				settingsURL := fmt.Sprintf("%s/settings?repo=%d", o.cfg.DashboardBaseURL, dbRepo.ID)
				body := fmt.Sprintf("Welcome to **Argus**! To enable AI code reviews, configure your API key and model at your [Argus Settings](%s).", settingsURL)
				if err := o.ghClient.CreateIssueComment(ctx, event.InstallationID, owner, repo, event.PRNumber, body); err != nil {
					o.logger.Error("posting onboarding comment", "error", err, "repo", event.RepoFullName)
				}
			}
			reviewID := uuid.New()
			// trace_id stays NULL here — no PipelineRun exists and the ctx-based
			// trace lift lands in Wave 2 (middleware writes it, orchestrator reads).
			if _, err := o.db.Exec(ctx, `
				INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, head_ref, status, trigger, error, trace_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'failed', 'webhook', 'no_api_key', NULL)
			`, reviewID, dbRepo.ID, event.PRNumber, event.PRTitle, event.PRAuthor, event.HeadSHA, event.BaseSHA, event.HeadRef); err != nil {
				o.logger.Error("recording skipped review", "error", err, "repo", event.RepoFullName)
			}
			return nil
		}
	}

	// Auto-run gate: webhook-driven events (opened/synchronize/reopened) honor
	// the repo/org auto_run flag (default ON, #161). Manual actions — the
	// @argus-eye review text command and checkbox-triggered runs — set
	// event.Action="manual" and bypass the gate as explicit user intent.
	// Self-hosted deployments always auto-run regardless of stored settings.
	//
	// When auto-run is disabled, every honored action (opened/synchronize/
	// reopened) emits the one-shot "Trigger Argus review" checkbox comment via
	// signalAutoRunDisabled — so a push is a visible signal, not a silent
	// no-op. Deduped to ONCE per PR (opened records the marker, later pushes
	// dedup against it), so repeated pushes don't spam.
	//
	// AUTO-RESOLVE exception: we fire auto-resolve BEFORE this gate on
	// every synchronize, because it's diff-only (no LLM, no BYOK cost) and
	// users on manual-review repos still expect stale comments to close
	// when they push a fix. See o.autoResolveOnSynchronize for the rules.
	//
	// Fail-closed on org-defaults load failure: if we can't read the
	// org config, we skip auto-resolve this round. Otherwise a transient
	// DB blip would ignore an explicit `auto_resolve_enabled=false` org
	// setting (the default=true would win), violating admin intent.
	orgDefaults, orgErr := o.st.GetOrgDefaults(ctx, inst.ID)
	if orgErr != nil {
		o.logger.Warn("loading org defaults", "error", orgErr, "installation", inst.ID)
	}
	// Resolve the incremental re-review plan ONCE per push. Both the
	// auto-resolve goroutine and the review path below need the inter-diff;
	// sharing one plan keeps the GetCompareCommitsDiff round-trip to a single
	// call per synchronize (previously each fetched it independently). Compute
	// only on synchronize, and only when a consumer will actually use it —
	// auto-resolve (enabled) or the review path (auto-run enabled) — so a
	// manual-review repo with auto-resolve off pays nothing.
	var incPlan *IncrementalPlan
	if event.Action == "synchronize" {
		autoResolveOn := orgErr == nil && IsAutoResolveEnabled(dbRepo.SettingsJSON, orgDefaults)
		if autoResolveOn || IsAutoRunEnabled(dbRepo.SettingsJSON, orgDefaults) {
			incPlan = o.incremental.Resolve(ctx, dbRepo.ID, event)
		}
		if autoResolveOn {
			go o.autoResolveOnSynchronize(ctx, event, inst.ID, dbRepo.ID, incPlan)
		}
	}

	// Pass the org-load state into the gate: on a transient GetOrgDefaults
	// failure a repo with no explicit auto_run must NOT apply the on-by-default
	// (we can't prove the org didn't opt out), so decideAutoRun fails CLOSED —
	// mirroring the auto-resolve gate above. A repo-explicit value still wins,
	// and self-hosted still short-circuits ON.
	switch decideAutoRun(o.cfg.SelfHosted, dbRepo.SettingsJSON, orgDefaults, orgErr != nil, event.Action) {
	case autoRunSignal:
		o.signalAutoRunDisabled(ctx, event, owner, repo, dbRepo)
		return nil
	case autoRunReview:
		// fall through to run the review pipeline
	}

	// On synchronize (new push), GitHub computes the diff asynchronously.
	// A brief delay avoids fetching stale/incomplete diff data and prevents
	// "submitted too quickly" 422 errors when posting the review later.
	if event.Action == "synchronize" {
		time.Sleep(3 * time.Second)
	}

	// Fetch diff — fall back to per-file API if GitHub returns 406 (diff too large)
	patchSet, rawDiff, err := o.fetchPRDiff(ctx, &event, owner, repo)
	if err != nil {
		return err
	}

	// Apply the shared incremental plan (resolved once above). A usable
	// inter-diff narrows the review to changes since the last completed review;
	// a fallback (fetch failure / empty compare / parse failure) was already
	// surfaced by the resolver as an "incremental.fallback" event, and we
	// proceed with the full diff fetched above.
	var isIncremental bool
	var previousReviewID *uuid.UUID
	if incPlan != nil && incPlan.IsIncremental {
		patchSet = incPlan.InterDiffPatch
		rawDiff = incPlan.InterDiffRaw
		isIncremental = true
		previousReviewID = incPlan.PreviousReviewID
	}

	// Auto-resolve stale bot comments fires once per push from
	// autoResolveOnSynchronize above, before the auto-run gate. It runs
	// regardless of whether the review pipeline reaches this point, so
	// there's no review-path trigger to maintain here.

	// Create review record
	reviewID := uuid.New()

	// Open SSE topic for live streaming
	if o.eventBus != nil {
		o.eventBus.OpenTopic(reviewID)
		defer o.eventBus.CloseTopic(reviewID)
	}

	// X-Argus-Trace-Id is lifted onto ctx by api.traceIDMiddleware (and the
	// webhook handler's SetTraceID on the detached ctx). Empty on direct
	// internal callers → SQL NULL via strPtrOrNil.
	traceID := obs.TraceID(ctx)
	_, err = o.db.Exec(ctx, `
		INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, head_ref, status, trigger, resolved_stale_count, trace_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', 'webhook', 0, $9)
	`, reviewID, dbRepo.ID, event.PRNumber, event.PRTitle, event.PRAuthor, event.HeadSHA, event.BaseSHA, event.HeadRef, strPtrOrNil(traceID))
	if err != nil {
		return fmt.Errorf("creating review record: %w", err)
	}

	// Resolve per-org memory indexer
	indexer := o.resolveIndexer(ctx, inst.ID)
	if indexer != nil {
		_, _ = o.db.Exec(ctx, `UPDATE reviews SET memory_enabled = true WHERE id = $1`, reviewID)
	}

	// Build the run (merged settings + prompts + feature flags + contract).
	run := o.buildRun(ctx, buildRunInput{
		reviewID:         reviewID,
		event:            event,
		patchSet:         patchSet,
		rawDiff:          rawDiff,
		dbInstallationID: inst.ID,
		dbRepoID:         dbRepo.ID,
		traceID:          traceID,
		isIncremental:    isIncremental,
		previousReviewID: previousReviewID,
		indexer:          indexer,
	})

	// Contract was computed inside buildRun from deterministic metadata (draft
	// flag, labels, branch prefix, changed paths, title, size). When metadata is
	// silent it stays "llm-pending" and the intent stage fills the change class.
	// Consumers (triage, review fan-out, pass2, posting) read it.
	o.logger.Info("review contract computed",
		"change_class", run.Contract.ChangeClass,
		"evidence_bar", run.Contract.EvidenceBar,
		"depth", run.Contract.Depth,
		"scrutiny_bump", run.Contract.ScrutinyBump,
		"unreviewable", run.Contract.Unreviewable,
		"signals", strings.Join(run.Contract.Signals, ","),
		"source", run.Contract.Source,
		"pr", event.PRNumber)

	// Attach prior review comments for incremental reviews so the LLM can avoid
	// duplicating previously-flagged issues and verify fixes. The resolver
	// already aggregated (and deduped) them across ALL completed reviews on the
	// PR — not just the most-recent — as part of the shared plan.
	if isIncremental && incPlan != nil {
		if pc := incPlan.PriorComments; len(pc) > 0 {
			run.PriorComments = pc
			o.logger.Info("loaded prior review comments for incremental review",
				"previous_review_id", previousReviewID,
				"files_with_comments", len(pc))
		}
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

	// Incremental graph indexing for changed files (non-blocking, 10s timeout)
	graphCtx, graphCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	go func() {
		defer graphCancel()
		defer func() {
			if r := recover(); r != nil {
				o.logger.Error("[graph] incremental index panic", "recover", r, "pr", event.PRNumber)
				emitPipelinePanicEvent(graphCtx, o.logger, "graph_incremental_index", r, obs.TraceID(graphCtx))
			}
		}()
		changedFiles := diffFilePaths(patchSet)
		if len(changedFiles) == 0 {
			return
		}
		if err := graph.IndexFiles(graphCtx, o.st, o.ghClient, event.InstallationID, owner, repo, event.HeadSHA, dbRepo.ID, changedFiles); err != nil {
			o.logger.Warn("[graph] incremental index failed", "error", err, "pr", event.PRNumber)
		} else {
			o.logger.Info("[graph] incremental index done", "files", len(changedFiles), "pr", event.PRNumber)
		}
	}()

	// Pre-review context enrichers: SAST hints, architecture context, linked
	// issues/PRs + feature flags, and author intent. Each is best-effort and
	// writes onto run (see prereview.go). This is the SAME sequence the
	// retry-rebuild paths run, so a retried review assembles identical
	// intent-aware context instead of skipping these islands.
	o.enrichPreReview(ctx, run)

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

	trigger := deriveTrigger(event.Action, isIncremental)
	o.logger.InfoContext(ctx, "review started",
		slog.String("event", "review.started"),
		slog.String("review_id", run.ReviewID.String()),
		slog.Int64("installation_id", inst.ID),
		slog.String("repo", event.RepoFullName),
		slog.Int("pr_number", event.PRNumber),
		slog.Bool("deep_review", run.DeepReview),
		slog.String("trigger", trigger),
		slog.String("trace_id", run.TraceID),
	)

	startTime := time.Now()
	err = o.sm.Run(ctx, run)
	durationMs := time.Since(startTime).Milliseconds()

	if err != nil {
		o.logger.ErrorContext(ctx, "review failed",
			slog.String("event", "review.failed"),
			slog.String("review_id", run.ReviewID.String()),
			slog.Int64("installation_id", inst.ID),
			slog.String("repo", event.RepoFullName),
			slog.Int("pr_number", event.PRNumber),
			slog.String("stage", string(run.State)),
			slog.String("error_class", classifyPipelineError(err)),
			slog.Int64("duration_ms", durationMs),
			slog.String("trace_id", run.TraceID),
		)
	} else {
		score := 0
		if run.Synthesis != nil && run.Synthesis.Score > 0 {
			score = run.Synthesis.Score
		}
		commentCount := 0
		for _, fr := range run.FileReviews {
			commentCount += len(fr.Comments)
		}
		o.logger.InfoContext(ctx, "review completed",
			slog.String("event", "review.completed"),
			slog.String("review_id", run.ReviewID.String()),
			slog.Int64("installation_id", inst.ID),
			slog.String("repo", event.RepoFullName),
			slog.Int("pr_number", event.PRNumber),
			slog.Int("score", score),
			slog.Int("comment_count", commentCount),
			slog.Int64("duration_ms", durationMs),
			slog.String("trace_id", run.TraceID),
		)
	}
	return err
}

// deriveTrigger maps a webhook action + incremental flag onto a short label
// used in the `trigger` slog attr. The mapping is the product's view, not
// GitHub's: every automatic run from a push/open/reopen is "webhook"; explicit
// manual runs ("manual" action from the trigger comment / slash command) are
// "manual"; and a follow-up diff on the same PR is "incremental".
func deriveTrigger(action string, isIncremental bool) string {
	if isIncremental {
		return "incremental"
	}
	if action == "manual" {
		return "manual"
	}
	return "webhook"
}

// classifyPipelineError maps a pipeline error returned from StateMachine.Run
// onto the same bucket vocabulary llm.classifyLLMError uses so PostHog
// dashboards can aggregate review.failed and llm.call.failed by error_class.
// Context-cancellation errors from user-initiated cancels surface as
// "cancelled" — distinct from timeouts because the user intentionally stopped
// the run rather than the pipeline blowing a budget.
func classifyPipelineError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "other"
}

// postTriggerComment renders the cost estimate + one-shot task-list checkbox
// ("Trigger Argus review") and posts it as an issue comment on the PR.
//
// Called once per PR lifetime — only on action=opened — when the auto_run flag
// is off for the repo/org. On subsequent pushes (synchronize) we silent-skip;
// users re-trigger via @argus-eye review or by clicking the existing checkbox.
//
// Estimate construction is best-effort (see BuildEstimate). A comment is
// always posted even if both historical + live lookups fail — in that case the
// body degrades to checkbox-only.
func (o *Orchestrator) postTriggerComment(ctx context.Context, event ghpkg.PREvent, owner, repo string, dbRepo *store.Repo) error {
	est := BuildEstimate(ctx, o.st, o.ghClient, event.InstallationID, dbRepo.ID, owner, repo, event.PRNumber, 0, o.logger)
	body := BuildTriggerComment(est, o.cfg.GitHubAppSlug)
	if err := o.ghClient.CreateIssueComment(ctx, event.InstallationID, owner, repo, event.PRNumber, body); err != nil {
		return err
	}
	o.logger.Info("trigger comment posted", "repo", event.RepoFullName, "pr", event.PRNumber, "files", est.Files, "diff_lines", est.DiffLines, "sample_size", est.SampleSize)
	return nil
}

// autoRunDisabledMarker is the reviews.error code recorded once per PR the
// first time the trigger affordance is posted for an auto-run-disabled repo.
// It is the dedup key (mirroring the no_api_key onboarding marker) that keeps
// later events from re-posting the trigger comment. Marker rows are excluded
// from dashboard list/stats reads — see isMarkerReview in the store.
const autoRunDisabledMarker = "auto_run_disabled"

// signalAutoRunDisabled emits the on-demand "Trigger review" affordance when
// auto-run is off (#161), so a honored event (opened/synchronize/reopened) is a
// visible signal rather than a silent no-op. It is idempotent: the comment is
// posted at most once per PR, deduped on a recorded marker review row, so
// opened + later pushes yield a single comment. Everything here is best-effort
// — a GitHub or DB failure logs and returns without failing the webhook.
func (o *Orchestrator) signalAutoRunDisabled(ctx context.Context, event ghpkg.PREvent, owner, repo string, dbRepo *store.Repo) {
	already, err := o.st.HasFailedReviewWithError(ctx, dbRepo.ID, event.PRNumber, autoRunDisabledMarker)
	if err != nil {
		o.logger.Error("checking prior auto-run-disabled signal", "error", err, "repo", event.RepoFullName, "pr", event.PRNumber)
	}
	if already {
		o.logger.Info("auto-run disabled; trigger affordance already posted, skipping", "repo", event.RepoFullName, "pr", event.PRNumber, "action", event.Action)
		return
	}
	if err := o.postTriggerComment(ctx, event, owner, repo, dbRepo); err != nil {
		o.logger.Error("posting auto-run-disabled trigger affordance", "error", err, "repo", event.RepoFullName, "pr", event.PRNumber)
		return
	}
	// Record the marker only after the comment posts so a failed post retries
	// on the next event instead of being permanently suppressed.
	reviewID := uuid.New()
	if _, err := o.db.Exec(ctx, `
		INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, head_ref, status, trigger, error, trace_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'failed', 'webhook', $9, NULL)
	`, reviewID, dbRepo.ID, event.PRNumber, event.PRTitle, event.PRAuthor, event.HeadSHA, event.BaseSHA, event.HeadRef, autoRunDisabledMarker); err != nil {
		o.logger.Error("recording auto-run-disabled signal", "error", err, "repo", event.RepoFullName)
	}
	o.logger.Info("auto-run disabled; posted trigger affordance", "repo", event.RepoFullName, "pr", event.PRNumber, "action", event.Action)
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

	// Gauge: mark posted findings addressed/ignored/deferred. Async because it
	// makes GitHub compare/commit calls; detached from ctx because the webhook
	// goroutine cancels it as soon as HandlePREvent returns. Non-fatal always.
	gaugeCtx, gaugeCancel := context.WithTimeout(
		obs.SetTraceID(context.Background(), obs.TraceID(ctx)), 2*time.Minute)
	go func() {
		defer gaugeCancel()
		defer func() {
			if r := recover(); r != nil {
				o.logger.Error("[gauge] detection panic", "recover", r, "pr", event.PRNumber)
			}
		}()
		o.detectFindingOutcomes(gaugeCtx, event, dbRepo.ID)
	}()

	if event.Merged {
		if err := o.st.MarkNodesMerged(ctx, dbRepo.ID, event.PRNumber); err != nil {
			o.logger.Error("[closed] failed to mark nodes merged", "error", err, "pr", event.PRNumber, "repo", event.RepoFullName)
			return nil // non-fatal for webhook response
		}
		o.logger.Info("[closed] PR merged — nodes marked permanent", "pr", event.PRNumber, "repo", event.RepoFullName)
		// No full re-index on merge. MarkNodesMerged above flips node
		// permanence in place; the files changed in this PR were already
		// indexed incrementally during review. A full 890-file re-parse
		// here OOM'd a 512 MB VM (~400 MB RSS peak) every time a large
		// repo merged. If drift repair is ever needed, run it as an
		// explicit admin/scheduled job, not a per-webhook goroutine.
	} else {
		if err := o.st.DeleteUnmergedNodesByPR(ctx, dbRepo.ID, event.PRNumber); err != nil {
			o.logger.Error("[closed] failed to delete unmerged nodes", "error", err, "pr", event.PRNumber, "repo", event.RepoFullName)
			return nil
		}
		o.logger.Info("[closed] PR closed without merge — unmerged nodes removed", "pr", event.PRNumber, "repo", event.RepoFullName)
	}
	return nil
}

// RetryReview re-runs a review's pipeline.
//
// A run still mid-flight is resumed from where it stopped (mirrors crash
// recovery). A run that already reached a terminal state (failed/cancelled)
// cannot be resumed — StateMachine.Run's loop returns immediately on a
// terminal state, so Resume would be a silent no-op and the review would sit
// on "pending" forever. For those we rebuild a fresh run for the SAME review
// and drive it from the initial state.
func (o *Orchestrator) RetryReview(ctx context.Context, reviewID uuid.UUID) error {
	// Topic may already be opened by the caller (retry handler opens it
	// before spawning the goroutine so the WebSocket can subscribe immediately).
	if o.eventBus != nil {
		o.eventBus.OpenTopic(reviewID)
	}

	var runID uuid.UUID
	err := o.db.QueryRow(ctx,
		`SELECT id FROM pipeline_states WHERE review_id = $1 ORDER BY updated_at DESC LIMIT 1`,
		reviewID,
	).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		// No persisted run: the review died before its first pipeline_states
		// persist (e.g. a restart during an early, non-persisted stage). There's
		// nothing to rebuild from, so reconstruct a fresh run from the review +
		// repo rows and fetch the diff fresh from GitHub.
		return o.retryFromReviewRow(ctx, reviewID)
	}
	if err != nil {
		return fmt.Errorf("finding pipeline run for review %s: %w", reviewID, err)
	}

	prev, err := o.sm.loadState(ctx, runID)
	if err != nil {
		return fmt.Errorf("loading pipeline run %s: %w", runID, err)
	}

	// Non-terminal run: resume in place. KNOWN GAP: a resumed run still loses
	// every json:"-" context field (intent/contract/SAST/arch/links/flags/
	// thresholds) — enriching mid-flight risks double-charging the intent LLM
	// call, so the resume ingress needs its own design (tracked follow-up).
	if !prev.State.IsTerminal() {
		_, err = o.sm.Resume(ctx, runID)
		return err
	}

	// Terminal run: rebuild and run from the initial state. Resume/OpenTopic
	// bookkeeping stays with the caller (retry handler owns CloseTopic).
	fresh, err := buildRetryRun(prev)
	if err != nil {
		return err
	}
	fresh.EventBus = o.eventBus
	// Resolve the live-only dependencies the sibling no-run retry path gets from
	// buildRun: the memory indexer never survives persistence (without it the
	// retry silently runs memory-less), and buildRetryRun's WithDefaults
	// fallback carries fixed-policy floors, not the org's settings-tuned ones —
	// re-resolve from merged settings, keeping the fallback on error.
	fresh.Indexer = o.resolveIndexer(ctx, fresh.DBInstallationID)
	if mergedSettings, msErr := o.st.GetMergedSettings(ctx, fresh.DBInstallationID, fresh.DBRepoID); msErr == nil {
		fresh.Thresholds = parseThresholds(mergedSettings)
	} else {
		o.logger.Error("retry: failed to load merged settings, using default thresholds",
			"error", msErr, "installation", fresh.DBInstallationID, "repo", fresh.DBRepoID)
	}
	// Re-run the pre-review enrichers so the retry resolves intent (and its
	// change class) and re-attaches SAST/arch/link context — none of which
	// survive persistence (all json:"-"). Without this a retried review posts
	// against an empty contract and loses intent-aware review context.
	o.enrichPreReview(ctx, fresh)
	return o.sm.Run(ctx, fresh)
}

// retryPREvent stamps a live-fetched PR event with the fields that come from our
// own records — the manual-trigger action and the GitHub repo id — for retrying
// a review that has no persisted run. SHAs, title, author, and refs come from
// the live PR fetch (livePR), NOT the stored review row, so the commit
// PostReview pins always matches the freshly-fetched diff even if the PR
// advanced (push/force-push) since the review row was written.
func retryPREvent(livePR *ghpkg.PREvent, repo *store.Repo) ghpkg.PREvent {
	event := *livePR
	event.Action = "manual"
	event.RepoID = repo.GithubID
	event.RepoFullName = repo.FullName
	return event
}

// retryFromReviewRow rebuilds and runs a fresh pipeline for a review that has no
// persisted pipeline_states run to rebuild from — it died before the first state
// persist (e.g. a restart during an early, non-persisted stage). It fetches the
// PR's CURRENT metadata + diff fresh from GitHub (the manual-trigger
// construction path) and runs a fresh run for the SAME review id.
//
// Current (not stored) SHAs are essential: the PR may have advanced since the
// review row was written, and pinning PostReview to a stale head while the diff
// is computed at the new head yields GitHub 422s / misplaced comments and
// degrades LLM file-content context. Stored SHAs are informational only.
//
// All RetryReview guards still hold: EnsureNotRunning trivially passed (no
// rows), the retry handler holds the per-PR slot + registered the cancel fn, and
// the run uses the same cooperative-cancel / conditional-write machinery. On any
// failure (including the metadata/diff fetch) the handler goroutine rolls the
// review back to "failed" — we never proceed with mismatched SHAs.
func (o *Orchestrator) retryFromReviewRow(ctx context.Context, reviewID uuid.UUID) error {
	review, err := o.st.GetReview(ctx, reviewID)
	if err != nil {
		return fmt.Errorf("loading review %s for no-run retry: %w", reviewID, err)
	}
	repo, err := o.st.GetRepo(ctx, review.RepoID)
	if err != nil {
		return fmt.Errorf("loading repo %d for no-run retry: %w", review.RepoID, err)
	}
	inst, err := o.st.GetInstallation(ctx, repo.InstallationID)
	if err != nil {
		return fmt.Errorf("loading installation %d for no-run retry: %w", repo.InstallationID, err)
	}

	owner, repoName, err := splitRepoFullName(repo.FullName)
	if err != nil {
		return err
	}

	// Fetch CURRENT PR metadata so event.HeadSHA/BaseSHA/refs match the diff
	// fetched below. Fail (→ rollback) rather than proceed with stale SHAs.
	livePR, err := o.ghClient.GetPullRequest(ctx, inst.InstallationID, owner, repoName, review.PRNumber)
	if err != nil {
		return fmt.Errorf("no-run retry %s: fetching current PR metadata: %w", reviewID, err)
	}
	event := retryPREvent(livePR, repo)

	patchSet, rawDiff, err := o.fetchPRDiff(ctx, &event, owner, repoName)
	if err != nil {
		return fmt.Errorf("no-run retry %s: %w", reviewID, err)
	}

	// Carry prior completed-review comments so this full re-review dedups
	// against / verifies findings already posted to the PR instead of
	// re-posting duplicates. Uses ResolvePriors (not Resolve): priors aggregate
	// across ALL completed reviews on the PR, but a retry is not a push, so it
	// must NOT fetch an inter-diff it would discard nor emit an
	// "incremental.fallback" signal. The review being retried isn't completed,
	// so it can't be its own prior; the id guard is belt-and-suspenders.
	plan := o.incremental.ResolvePriors(ctx, review.RepoID, review.PRNumber)
	var priorComments map[string][]PriorComment
	isIncremental := false
	var previousReviewID *uuid.UUID
	if len(plan.PriorComments) > 0 && (plan.PreviousReviewID == nil || *plan.PreviousReviewID != reviewID) {
		priorComments = plan.PriorComments
		isIncremental = true
		previousReviewID = plan.PreviousReviewID
		o.logger.Info("no-run retry: carried prior review comments",
			"previous_review_id", previousReviewID, "files_with_comments", len(priorComments))
	}

	// Settings/feature flags come from the repo's current merged settings via
	// buildRun. We fetched the whole PR diff, so this is a full re-review unless
	// a prior completed review's comments engage the incremental dedup guards.
	run := o.buildRun(ctx, buildRunInput{
		reviewID:         reviewID,
		event:            event,
		patchSet:         patchSet,
		rawDiff:          rawDiff,
		dbInstallationID: repo.InstallationID,
		dbRepoID:         review.RepoID,
		traceID:          obs.TraceID(ctx),
		isIncremental:    isIncremental,
		previousReviewID: previousReviewID,
		indexer:          o.resolveIndexer(ctx, repo.InstallationID),
	})
	run.PriorComments = priorComments
	// Same pre-review enrichment the fresh webhook path runs, so a no-run retry
	// resolves intent (+ change class) and re-attaches SAST/arch/link context
	// instead of driving the pipeline off an empty contract.
	o.enrichPreReview(ctx, run)
	return o.sm.Run(ctx, run)
}

// buildRetryRun constructs a fresh PipelineRun for retrying a review whose
// prior run reached a terminal state. It carries the immutable review inputs
// (PR event, diff, resolved feature settings, prompts) onto a brand-new run
// with a new run ID and the initial state, dropping every intermediate stage
// result and the terminal error so the pipeline starts clean.
//
// The review ID is preserved — this is a retry of the SAME review, not a new
// one. Contract is recomputed from the carried PR event + diff because it is
// marked json:"-" and never survived persistence.
//
// Returns a descriptive error when the persisted PR event is unusable (empty
// repo full name or zero PR number): without it triage/review/posting have
// nothing to act on and the run would fail deeper in the pipeline.
func buildRetryRun(prev *PipelineRun) (*PipelineRun, error) {
	if prev.PREvent.RepoFullName == "" || prev.PREvent.PRNumber == 0 {
		return nil, fmt.Errorf(
			"cannot retry review %s: persisted run has no usable PR event (repo=%q, pr=%d)",
			prev.ReviewID, prev.PREvent.RepoFullName, prev.PREvent.PRNumber)
	}

	var files []diff.FileDiff
	if prev.Diff != nil {
		files = prev.Diff.Files
	}

	now := time.Now()
	fresh := &PipelineRun{
		ID:       uuid.New(),
		ReviewID: prev.ReviewID,
		State:    StatePending,
		PREvent:  prev.PREvent,

		DBInstallationID: prev.DBInstallationID,
		DBRepoID:         prev.DBRepoID,
		TraceID:          prev.TraceID,

		Diff:    prev.Diff,
		RawDiff: prev.RawDiff,

		Persona:             prev.Persona,
		CustomPersonaPrompt: prev.CustomPersonaPrompt,
		DeepReview:          prev.DeepReview,
		CrossFileContext:    prev.CrossFileContext,
		BlastRadius:         prev.BlastRadius,
		ScenarioMemory:      prev.ScenarioMemory,
		CodeSimulation:      prev.CodeSimulation,
		PREnrichment:        prev.PREnrichment,
		LearnPatterns:       prev.LearnPatterns,
		LearnConventions:    prev.LearnConventions,
		FileSynthesis:       prev.FileSynthesis,
		ArchitectureGraph:   prev.ArchitectureGraph,

		Prompts: prev.Prompts,
		// Normalize the similarity gates at this single retry-construction ingress
		// so value-level readers never see a zero Thresholds. prev.Thresholds is
		// json:"-" (never persisted), so a terminal run loaded from the DB carries
		// the zero value; WithDefaults resolves it to the fixed-policy defaults —
		// the same numbers the scattered per-reader WithDefaults calls would yield.
		Thresholds:       prev.Thresholds.WithDefaults(),
		IsIncremental:    prev.IsIncremental,
		PreviousReviewID: prev.PreviousReviewID,
		// Carry prior-review comments so an incremental retry keeps dedup/verify
		// context and doesn't re-post every previously-flagged finding.
		PriorComments: prev.PriorComments,

		CreatedAt: now,
		UpdatedAt: now,
	}
	fresh.Contract = ComputeContract(&fresh.PREvent, files)
	return fresh, nil
}

// EnsureNotRunning and CancelStranded delegate to the ReviewLifecycle, the
// single home for the review-lifecycle DB guards (see lifecycle.go). The thin
// wrappers keep the existing api.Server call sites (s.orchestrator.*) intact.

// EnsureNotRunning returns ErrReviewRunning when the review's latest pipeline
// run looks live, so a retry can be refused (→ 409) instead of double-running.
func (o *Orchestrator) EnsureNotRunning(ctx context.Context, reviewID uuid.UUID) error {
	return o.lifecycle.EnsureNotRunning(ctx, reviewID)
}

// CancelStranded marks a review (and its latest run) cancelled when no in-memory
// cancel function is available — a process restart or a cross-machine cancel.
func (o *Orchestrator) CancelStranded(ctx context.Context, reviewID uuid.UUID) error {
	return o.lifecycle.CancelStranded(ctx, reviewID)
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

	body := fmt.Sprintf("> **Argus** is reviewing this PR — [watch live](%s/reviews/%s)\n\n| | |\n|---|---|\n%s",
		o.cfg.DashboardBaseURL, run.ReviewID, strings.Join(rows, "\n"))

	nodeID, err := o.ghClient.CreateIssueCommentWithNodeID(ctx, event.InstallationID, owner, repo, event.PRNumber, body)
	if err != nil {
		o.logger.Warn("failed to post review-started comment", "error", err)
		return
	}
	run.StartedCommentNodeID = nodeID
}

// autoResolveOnSynchronize fires fire-and-forget on every synchronize
// push where auto-resolve is enabled, independently of the review
// pipeline's auto-run gate. Runs in its own goroutine (launched by the
// caller) with a 30s GitHub timeout and a separate 5s DB-insert timeout
// so a slow GitHub path can't lose the stats row.
//
// Uses context.WithoutCancel so the goroutine survives the parent
// handler returning — the handler's ctx typically cancels when the
// webhook HTTP response is sent, but auto-resolve is useful long
// after that.
//
// Early-returns when the shared plan carries no usable inter-diff: no prior
// review (first push), a fallback (fetch failure / empty compare / parse
// failure — already surfaced by the resolver as an "incremental.fallback"
// event), or an unchanged head. None of those are bugs here; they mean no
// lines could have moved, so there's nothing to resolve.
//
// Call site: `go o.autoResolveOnSynchronize(ctx, event, dbInstallationID, dbRepoID, plan)`.
// The plan is resolved ONCE per push by IncrementalResolver and shared with the
// review path, so the inter-diff GitHub round-trip fires once, not twice.
//
// IMPORTANT: dbInstallationID is the Argus DB's `installations.id` (BIGSERIAL
// primary key), NOT the GitHub installation ID on `event.InstallationID`.
// The auto_resolve_events.installation_id FK points at the DB's own PK, so
// passing event.InstallationID produces an SQLSTATE 23503 violation on every
// insert.
func (o *Orchestrator) autoResolveOnSynchronize(
	parent context.Context,
	event ghpkg.PREvent,
	dbInstallationID int64,
	dbRepoID int64,
	plan *IncrementalPlan,
) {
	defer func() {
		if r := recover(); r != nil {
			o.logger.Error("autoResolveOnSynchronize panic",
				"recover", r,
				"stack", string(debug.Stack()),
				"pr", event.PRNumber)
			emitPipelinePanicEvent(parent, o.logger, "auto_resolve", r, obs.TraceID(parent))
		}
	}()

	// Reuse the inter-diff the resolver already fetched + parsed for the review
	// path. No usable patch (no prior review, fallback, or unchanged head) means
	// nothing whose lines could have moved — quiet return.
	if plan == nil || plan.InterDiffPatch == nil {
		return
	}
	patchSet := plan.InterDiffPatch

	ghCtx, ghCancel := context.WithTimeout(
		context.WithoutCancel(parent), 30*time.Second)
	defer ghCancel()

	// ThreadRegistry (#162): the prior review's hydrated thread links let
	// auto-resolve prefer the stored (authoritative) node id over the live
	// list's, falling back to proximity for rows predating the registry. The
	// previous-review id comes from the shared plan — a non-nil InterDiffPatch
	// guarantees Resolve set PreviousReviewID (they're set together), so the
	// deref is safe and we avoid re-fetching the prior review here.
	storedThreadIDs := o.storedThreadIDsForReview(ghCtx, *plan.PreviousReviewID)
	resolved, attempted, botUnresolved, apiCalls, resolvedKeys := o.autoResolveStaleComments(ghCtx, event, patchSet, storedThreadIDs)
	// Persist whenever we touched GitHub — even a list-only call (0
	// resolved, 0 attempted, 1 apiCall) is load operators may need to
	// see against their installation token budget.
	if resolved+attempted+apiCalls == 0 {
		return
	}

	// Separate short context for the DB insert so a slow GitHub path
	// (near the 30s ceiling) can't cost us the stats row.
	dbCtx, dbCancel := context.WithTimeout(
		context.WithoutCancel(parent), 5*time.Second)
	defer dbCancel()

	if err := o.st.InsertAutoResolveEvent(dbCtx, store.InsertAutoResolveEventParams{
		InstallationID:     dbInstallationID,
		RepoID:             dbRepoID,
		PRNumber:           event.PRNumber,
		SourceSHA:          event.HeadSHA,
		ResolvedCount:      resolved,
		AttemptedCount:     attempted,
		GitHubAPICalls:     apiCalls,
		ResolvedThreadKeys: resolvedKeys,
	}); err != nil {
		o.logger.Warn("auto-resolve: persist event",
			"error", err, "pr", event.PRNumber)
	}

	// auto_resolve.evaluated fires on every sync where we actually touched
	// GitHub — threads_checked is stale-comment pressure (every open Argus
	// thread we considered), threads_attempted is how many we tried to
	// close, threads_resolved is the subset that actually closed on the GH
	// API. Keeping all three lets the PostHog funnel distinguish "nothing
	// to do" from "tried and failed". trace_id comes from the detached
	// parent ctx: handleWebhook set it via SetTraceID before detaching, so
	// the trace survives past the webhook response.
	o.logger.InfoContext(parent, "auto-resolve evaluated",
		slog.String("event", "auto_resolve.evaluated"),
		slog.Int64("installation_id", dbInstallationID),
		slog.String("repo", event.RepoFullName),
		slog.Int("pr_number", event.PRNumber),
		slog.Int("threads_checked", botUnresolved),
		slog.Int("threads_attempted", attempted),
		slog.Int("threads_resolved", resolved),
		slog.String("trace_id", obs.TraceID(parent)),
	)
}

// autoResolveDecisionKind classifies the outcome of decideAutoResolveThread.
type autoResolveDecisionKind int

const (
	autoResolveSkip    autoResolveDecisionKind = iota // leave thread alone
	autoResolveLineHit                                // changed line within ±lineProximity
	autoResolveFileHit                                // file-level fallback (no line info)
)

// autoResolveDecision is the pure output of the thread-level resolution
// rule. joinKey is non-empty only for line-addressed resolutions (file-level
// fallbacks contribute nothing to the migration-041 join array because a
// "path:0" key can never match any Finding).
type autoResolveDecision struct {
	kind    autoResolveDecisionKind
	joinKey string // "<path>:<line>" or ""
}

// decideAutoResolveThread is the pure decision rule: given a review thread
// and the set of files+lines changed in the new push, should the thread be
// auto-resolved, and under what classification?
//
// Extracted from autoResolveStaleComments so the decision can be exercised
// without a GitHub fake. The caller still owns the actual ResolveReviewThread
// call, counter bookkeeping, and Argus-author gating.
func decideAutoResolveThread(
	t ghpkg.ReviewThread,
	changedFiles map[string]bool,
	fileChangedLines map[string]map[int]bool,
	lineProximity int,
) autoResolveDecision {
	if !changedFiles[t.Path] {
		return autoResolveDecision{kind: autoResolveSkip}
	}
	changedLineSet := fileChangedLines[t.Path]
	if t.Line > 0 && len(changedLineSet) > 0 {
		// Shared Matcher with the auto-resolve policy {Proximity: lineProximity,
		// UseCategory: false}: a changed line within the window resolves the
		// thread regardless of category (threads carry none). Path is equal on
		// both anchors (changedLineSet is fileChangedLines[t.Path]).
		matcher := Matcher{Proximity: lineProximity, UseCategory: false}
		threadAnchor := Anchor{Path: t.Path, Line: t.Line}
		for changedLine := range changedLineSet {
			if matcher.Matches(threadAnchor, Anchor{Path: t.Path, Line: changedLine}) {
				return autoResolveDecision{
					kind:    autoResolveLineHit,
					joinKey: fmt.Sprintf("%s:%d", t.Path, t.Line),
				}
			}
		}
		return autoResolveDecision{kind: autoResolveSkip}
	}
	// No line info (file-level thread) or no parsed hunks — fall back to
	// file-level. No joinKey: Finding is always line-addressed.
	return autoResolveDecision{kind: autoResolveFileHit}
}

// resolvedByReplyBody renders the one-line convergence breadcrumb posted on a
// thread before auto-resolving it. Pure — headSHA is shortened to 7 chars; an
// empty SHA (defensive: PREvent built from a partial payload) degrades to a
// sha-less line rather than rendering an empty code span.
func resolvedByReplyBody(headSHA string) string {
	short := headSHA
	if len(short) > 7 {
		short = short[:7]
	}
	if short == "" {
		return "✅ Resolved by a newer push — the flagged lines were modified."
	}
	return fmt.Sprintf("✅ Resolved by `%s` — the flagged lines were modified in this push.", short)
}

// autoResolveStaleComments resolves bot review threads whose specific lines
// were modified in the new push. Uses line-level checking: a thread is resolved
// only if the changed lines in the inter-diff overlap with the comment's line
// range (within a proximity window). Falls back to file-level for threads with
// no line information (e.g., file-level comments).
//
// Identifies Argus threads by login equality only (via ghpkg.IsArgusThread)
// — the older `strings.HasSuffix(..., "[bot]")` heuristic silently closed
// threads from sibling bots (Cubic, Codecov, Dependabot, Renovate) whose
// comment lines happened to sit within the proximity window of a user's
// diff. Now we only touch our own threads.
//
// Returns (resolvedCount, attemptedCount, apiCalls). apiCalls counts every
// GitHub mutation we issued (ListReviewThreads + each ResolveReviewThread
// attempt, success or failure) — persisted on auto_resolve_events so
// operators can see rate-limit pressure.
// autoResolveStaleComments returns:
//   - resolved / attempted / apiCalls: aggregate counters for auto_resolve_events.
//   - botUnresolved: Argus threads found open on the PR before we decided which
//     to touch. Distinct from attempted (which only counts decisions in favour
//     of resolving) — feeds the PostHog "stale-comment pressure" funnel.
//   - resolvedKeys: "<path>:<line>" keys for each thread actually resolved.
//     Consumed by hydratePriorFindings via the migration-041 join column so
//     the async cross-PR stage can drop prior findings whose threads are
//     already closed. Keys use the resolved thread's Path + Line so the same
//     shape produced by Finding.Path + Finding.Line on the reader side
//     matches. Empty means nothing was resolved (legal: the push may have
//     just fired the goroutine against an already-clean PR).
//
// storedThreadIDs maps a finding's REST comment id → its GraphQL thread node id
// as hydrated at post time (ThreadRegistry, #162). When a thread we're about to
// resolve has a stored node id we prefer it (authoritative); threads without one
// (rows predating the registry, sibling bots) fall back to the live node id from
// ListReviewThreads. A nil/empty map preserves the pre-registry behaviour.
func (o *Orchestrator) autoResolveStaleComments(
	ctx context.Context,
	event ghpkg.PREvent,
	patchSet *diff.PatchSet,
	storedThreadIDs map[int64]string,
) (resolved, attempted, botUnresolved, apiCalls int, resolvedKeys []string) {
	owner, repo, err := splitRepoFullName(event.RepoFullName)
	if err != nil {
		o.logger.Warn("auto-resolve: bad repo name", "error", err)
		return 0, 0, 0, 0, nil
	}

	threads, err := o.ghClient.ListReviewThreads(ctx, event.InstallationID, owner, repo, event.PRNumber)
	apiCalls++ // count the list call regardless of outcome
	if err != nil {
		o.logger.Warn("auto-resolve: listing threads", "error", err)
		return 0, 0, 0, apiCalls, nil
	}

	// Build per-file changed line sets and a file-presence set
	changedFiles := make(map[string]bool)
	fileChangedLines := make(map[string]map[int]bool)
	const lineProximity = 3 // resolve if changed line is within 3 lines of comment
	for _, f := range patchSet.Files {
		changedFiles[f.NewName] = true
		fileChangedLines[f.NewName] = f.ChangedLines()
	}

	o.logger.Info("auto-resolve: found threads", "total", len(threads), "changed_files", len(changedFiles), "pr", event.PRNumber)

	var lineHit, fileHit int
	for _, t := range threads {
		if t.IsResolved || !ghpkg.IsArgusThread(t.AuthorLogin, o.cfg.GitHubAppSlug) {
			continue
		}
		botUnresolved++

		decision := decideAutoResolveThread(t, changedFiles, fileChangedLines, lineProximity)
		switch decision.kind {
		case autoResolveSkip:
			continue
		case autoResolveLineHit:
			lineHit++
		case autoResolveFileHit:
			fileHit++
		}

		attempted++
		// Convergence breadcrumb: a brief "resolved by <short-sha>" reply on
		// the thread BEFORE resolving it, so the author sees why the thread
		// closed. Best-effort — a failed reply never blocks the resolution.
		if t.FirstCommentID != 0 {
			apiCalls++
			if _, err := o.ghClient.ReplyToComment(ctx, event.InstallationID, owner, repo, event.PRNumber, t.FirstCommentID, resolvedByReplyBody(event.HeadSHA)); err != nil {
				o.logger.Warn("auto-resolve: resolved-by reply failed", "error", err, "thread_id", t.ID, "path", t.Path)
			}
		}
		// Prefer the ThreadRegistry-hydrated node id (keyed on the thread's
		// first-comment REST id) when present; else use the live list's node id.
		// The two are the same stable GraphQL node for our own threads, so this
		// is behaviour-preserving — it just routes resolution through the stored
		// link where one exists.
		threadID := t.ID
		if stored, ok := storedThreadIDs[t.FirstCommentID]; ok {
			threadID = stored
		}
		apiCalls++ // one mutation per attempt
		if err := o.ghClient.ResolveReviewThread(ctx, event.InstallationID, threadID); err != nil {
			o.logger.Warn("auto-resolve: resolve thread failed", "error", err, "thread_id", threadID, "path", t.Path)
			continue
		}
		resolved++
		if decision.joinKey != "" {
			resolvedKeys = append(resolvedKeys, decision.joinKey)
		}
		// comment.thread_resolved fires per successful thread close — volume
		// here can be high on a big push (tens per synchronize). We still
		// emit at Info so PostHog's funnel sees individual resolutions, but
		// the attrs deliberately exclude Path/body text (PII + size) — only
		// thread_id survives.
		o.logger.InfoContext(ctx, "comment thread resolved",
			slog.String("event", "comment.thread_resolved"),
			slog.Int("pr_number", event.PRNumber),
			slog.String("thread_id", t.ID),
			slog.String("trace_id", obs.TraceID(ctx)),
		)
	}

	o.logger.Info("auto-resolve complete",
		"resolved", resolved, "attempted", attempted,
		"bot_unresolved", botUnresolved,
		"line_level_hits", lineHit, "file_level_fallbacks", fileHit,
		"api_calls", apiCalls,
		"resolved_keys", len(resolvedKeys),
		"pr", event.PRNumber)
	return resolved, attempted, botUnresolved, apiCalls, resolvedKeys
}

// enrichFindings runs the per-finding memory-enrichment pass over
// run.FileReviews via the Enricher and applies its result: it merges the
// suppression keys into the run, then logs + publishes the aggregate. Non-fatal
// end-to-end — a disabled indexer or malformed repo name is a no-op, and the
// Enricher leaves novelty unset on any search error. The per-finding fan-out,
// self-match guard, pattern/rule linking, and suppression bookkeeping all live
// inside the Enricher (see enricher.go); this call site only builds deps and
// applies the result.
func (o *Orchestrator) enrichFindings(ctx context.Context, run *PipelineRun) error {
	if run.Indexer == nil {
		return nil
	}
	_, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return nil // non-fatal
	}

	// The contract change class drives the dismissal lifecycle filter; a nil
	// contract behaves as production everywhere else in the pipeline.
	changeClass := ""
	if run.Contract != nil {
		changeClass = run.Contract.ChangeClass
	}

	enricher := &Enricher{
		reader: run.Indexer,
		linker: o.enrichStoreDep(),
		logger: o.logger,
		// Default thresholds once for the whole pass — the deep Search reads take a
		// concrete float floor now that the reader seam no longer defaults internally.
		thresholds:   run.Thresholds.WithDefaults(),
		repo:         repo,
		repoFullName: run.PREvent.RepoFullName,
		prNumber:     run.PREvent.PRNumber,
		reviewID:     run.ReviewID,
		repoID:       run.DBRepoID,
		changeClass:  changeClass,
		traceID:      run.TraceID,
		concurrency:  enrichConcurrency,
	}
	if run.EventBus != nil {
		enricher.publish = func(evt EventType, data map[string]any) {
			run.EventBus.Publish(run.ReviewID, evt, data)
		}
	}

	res := enricher.Run(ctx, run.FileReviews)

	// Apply: record a snapshot-stable key for every dropped finding so
	// pattern-learning (which reads the pre-enrich AllFileReviews copy, without
	// the Suppressed flag) skips it and never re-learns it as a pattern.
	for k := range res.SuppressedKeys {
		if run.SuppressedKeys == nil {
			run.SuppressedKeys = make(map[string]struct{})
		}
		run.SuppressedKeys[k] = struct{}{}
	}

	o.logger.InfoContext(ctx, "enriched findings",
		slog.String("event", "memory.enriched"),
		slog.String("review_id", run.ReviewID.String()),
		slog.String("repo", run.PREvent.RepoFullName),
		slog.Int("pr_number", run.PREvent.PRNumber),
		slog.Int("matched", res.Matched),
		slog.Int("enforced", res.Enforced),
		slog.Int("novel", res.Novel),
		slog.Int("suppressed", res.Suppressed),
		slog.Int("downgraded", res.Downgraded),
		slog.Int("total", res.Total()))

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventFindingsEnriched, map[string]any{
			"matched":    res.Matched,
			"enforced":   res.Enforced,
			"novel":      res.Novel,
			"suppressed": res.Suppressed,
			"downgraded": res.Downgraded,
			"total":      res.Total(),
		})
	}
	return nil
}

// inferMatchKind derives the MatchedPatternKind from Supermemory metadata. The
// "source" key is stamped at index time and distinguishes what kind of document
// produced the match. Falls through to "similarity" when metadata is absent —
// e.g. older indexed docs pre-dating the metadata stamping.
func inferMatchKind(md map[string]string) string {
	switch md["source"] {
	case "scoring_confirmed", "auto_learn":
		return "pattern"
	case "convention_extraction", "convention":
		return "convention"
	case "pr_summary", "synthesis", "arch_summary":
		return "similarity"
	}
	return "similarity"
}

// metaInt reads an integer value stamped in Supermemory metadata. Missing /
// malformed values return 0 so the caller renders without a PR reference.
func metaInt(md map[string]string, key string) int {
	if v, ok := md[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

// metaAgeDays parses an ISO-8601 or RFC3339 timestamp from metadata and returns
// its age in whole days. 0 means "unknown / fresh / unparseable" — renderer
// silently skips the age clause rather than print "0 days ago".
func metaAgeDays(md map[string]string, key string) int {
	raw, ok := md[key]
	if !ok {
		return 0
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			days := int(time.Since(t).Hours() / 24)
			if days < 0 {
				return 0
			}
			return days
		}
	}
	return 0
}

func (o *Orchestrator) synthesize(ctx context.Context, run *PipelineRun) error {
	// Annotate findings with pattern matches and novelty flags before building summary
	if err := o.enrichFindings(ctx, run); err != nil {
		o.logger.Warn("enrichFindings failed, continuing", "error", err)
	}

	// Intent verification: decide whether the diff delivers the stated goal and
	// which findings fall in explicit non-goals. Demotion mutates FileReviews so
	// the score calculation below sees the adjusted severities.
	verdict := o.verifyIntent(ctx, run)
	if verdict != nil && len(verdict.OutOfScopeFindings) > 0 {
		unmatched, stale := DemoteOutOfScopeFindings(run, verdict)
		switch {
		case stale:
			// FileReviews was mutated between verdict construction and demotion —
			// positional IDs are invalid. Skip demotion rather than damage random
			// comments. This is a pipeline-ordering bug signal; log it loudly.
			o.logger.Warn("intent verification: stale verdict, skipping demotion",
				"built_against", verdict.BuiltAgainstCount,
				"current", countFlatComments(run),
				"pr", run.PREvent.PRNumber)
		default:
			o.logger.Info("intent verification demoted out-of-scope findings",
				"count", len(verdict.OutOfScopeFindings)-len(unmatched),
				"pr", run.PREvent.PRNumber)
			if len(unmatched) > 0 {
				// LLM hallucinated ids that don't map to any existing comment.
				// Log so drift shows up in fleet logs without being fatal.
				o.logger.Warn("intent verification: unmatched out-of-scope finding ids",
					"ids", unmatched, "pr", run.PREvent.PRNumber)
			}
		}
	}

	var summary strings.Builder
	verb := "Reviewed"
	if run.IsIncremental {
		verb = "Re-reviewed"
	}
	fileCount := len(run.Diff.Files)
	cmtCount := countComments(run)
	summary.WriteString(fmt.Sprintf("%s %d %s with %d %s.\n\n",
		verb,
		fileCount, pluralize("file", fileCount),
		cmtCount, pluralize("comment", cmtCount)))

	for _, fr := range run.FileReviews {
		summary.WriteString(fmt.Sprintf("### `%s`\n", fr.Path))
		for _, c := range fr.Comments {
			if c.Suppressed {
				continue // dropped by dismissal-match: never listed in the summary
			}
			desc := c.What
			if desc == "" {
				desc = c.Body
			}
			emoji := severityEmoji(c.Severity)
			summary.WriteString(fmt.Sprintf("- %s **[%s]** L%d: %s\n", emoji, c.Severity, c.Line, desc))
		}
		summary.WriteString("\n")
	}

	// Inject issue acceptance coverage (from validateStage's issueAcceptance worker).
	if section := formatIssueCoverageSection(run.IssueAcceptance); section != "" {
		summary.WriteString(section)
	}

	// Inject cross-repo PR coverage (from validateStage's crosspr worker).
	if section := formatCrossPRCoverageSection(run.CrossPRCoverage); section != "" {
		summary.WriteString(section)
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
				simScenarios[i] = SimScenario{ID: s.ID, Description: s.Description, Severity: s.Severity, Source: s.Source, Files: s.Files}
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
		n := len(run.Diff.Files)
		noun := "files"
		if n == 1 {
			noun = "file"
		}
		brief = fmt.Sprintf("Argus reviewed %d %s and found no issues. Code looks good.", n, noun)
	} else {
		// Try LLM-generated conversational brief
		brief = o.generateConversationalBrief(ctx, run, score)
	}

	// Capture the H2 headline BEFORE the intent header is prepended to brief —
	// otherwise the posted comment's H2 would pull the first sentence of the
	// intent disclaimer ("### 🔍 PR intent vs diff … _Argus read the diff …_")
	// instead of the actual synthesis verdict.
	//
	// Preferred path: the synthesis LLM was prompted to emit a dedicated
	// `**Headline:** …` line (≤100 chars, no markdown). When present we strip
	// it from the brief body and use it verbatim — no truncation needed.
	//
	// Fallback: if the LLM skipped or drifted on the Headline line, derive
	// one from the first sentence via extractHeadline (strips leading bold
	// prefix, truncates at word boundary).
	headline, briefBody := splitHeadlineAndBody(brief)
	if headline == "" {
		headline = extractHeadline(brief, 100)
	} else if utf8.RuneCountInString(headline) > 100 {
		// LLM ignored the 100-char constraint. Truncate at a word boundary
		// rather than ship an over-long H2.
		headline = extractHeadline(headline, 100)
	} else {
		// Headline was valid — body is the brief minus the Headline line.
		brief = briefBody
	}

	// Prepend intent header + [INTENT] finding; both return "" when not applicable.
	if header := FormatIntentHeader(run, verdict); header != "" {
		brief = header + "\n" + brief
	}
	finalSummary := summary.String()
	if intentFinding := FormatIntentFinding(verdict); intentFinding != "" {
		finalSummary = intentFinding + finalSummary
	}
	if shouldShowNoIntentCallout(run) {
		brief += NoIntentCallout
	}

	run.Synthesis = &SynthesisResult{
		Summary:           finalSummary,
		Brief:             brief,
		Headline:          headline,
		Score:             score,
		SimulationResults: simResults,
		IntentVerdict:     verdict,
	}

	if len(simResults) > 0 {
		run.Synthesis.Brief += FormatSimulationResults(simResults)
	}

	// Unconfigured-scoring notice — SINGLE append point. Summary is what gets
	// persisted to reviews.summary (dashboard); Brief opens the GitHub review
	// comment in post(). post() must NOT append again.
	if notice := scoringSkippedNotice(run); notice != "" {
		run.Synthesis.Summary += "\n\n" + notice
		run.Synthesis.Brief += "\n\n" + notice
	}

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventSynthesis, map[string]any{
			"summary": run.Synthesis.Summary,
			"score":   run.Synthesis.Score,
		})
	}
	return nil
}

// verifyIntent asks the LLM whether the diff delivers the PR's stated goal and
// which (if any) findings fall under the author's declared non_goals. Returns
// nil when there's no intent to verify, no provider available, or the LLM call
// / parse fails — intent verification is best-effort and never blocks synthesis.
func (o *Orchestrator) verifyIntent(ctx context.Context, run *PipelineRun) *IntentVerdict {
	if !run.PRIntent.HasIntent() {
		return nil
	}

	lister := storeConfigLister{st: o.st, installationID: run.DBInstallationID}
	provider, cfg, err := o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageSynthesis)
	if err != nil {
		provider, cfg, err = o.reviewStage.registry.ResolveProvider(ctx, lister, run.DBInstallationID, run.DBRepoID, llm.StageReview)
		if err != nil {
			o.logger.Warn("intent verification: no provider available",
				"error", err,
				"cancellation", errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded),
				"pr", run.PREvent.PRNumber)
			return nil
		}
	}

	vctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := provider.Complete(vctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      intentVerificationSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: BuildIntentVerificationPrompt(run)}},
		MaxTokens:   600,
		Temperature: 0.2,
		JSONMode:    true,
		Stage:       "intent_verify",
	})
	if err != nil {
		o.logger.Warn("intent verification: LLM call failed",
			"error", err,
			"cancellation", errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded),
			"pr", run.PREvent.PRNumber)
		return nil
	}

	tokens := StageTokens{
		PromptTokens:     resp.TokensUsed.PromptTokens,
		CompletionTokens: resp.TokensUsed.CompletionTokens,
		TotalTokens:      resp.TokensUsed.TotalTokens,
		Cost:             resp.Cost,
		Model:            cfg.Model,
		Provider:         cfg.Provider,
	}
	run.Tokens.Intent.PromptTokens += tokens.PromptTokens
	run.Tokens.Intent.CompletionTokens += tokens.CompletionTokens
	run.Tokens.Intent.TotalTokens += tokens.TotalTokens
	run.Tokens.Intent.Cost += tokens.Cost
	if run.Tokens.Intent.Model == "" {
		run.Tokens.Intent.Model = cfg.Model
		run.Tokens.Intent.Provider = cfg.Provider
	}
	run.Tokens.addToTotal(tokens)

	verdict, err := parseIntentVerdict(resp.Content)
	if err != nil {
		o.logger.Warn("intent verification: parse failed",
			"error", err,
			"model", cfg.Model,
			"finish_reason", resp.FinishReason,
			"response_prefix", util.Truncate(resp.Content, 300, true),
			"pr", run.PREvent.PRNumber)
		return nil
	}

	// Stamp the FileReviews count the verdict was built against so
	// DemoteOutOfScopeFindings can guard against positional-id drift.
	verdict.BuiltAgainstCount = countFlatComments(run)

	o.logger.Info("intent verified",
		"delivers", verdict.Delivers,
		"unmet", len(verdict.UnmetCriteria),
		"out_of_scope", len(verdict.OutOfScopeFindings),
		"pr", run.PREvent.PRNumber)

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventIntentVerified, map[string]any{
			"delivers":     verdict.Delivers,
			"unmet":        len(verdict.UnmetCriteria),
			"out_of_scope": len(verdict.OutOfScopeFindings),
		})
	}
	return verdict
}

const synthesisBriefSystemPrompt = `You are writing a concise verdict for a pull request review. Per-file inline comments are shown separately — do NOT repeat them.

Format (markdown) — emit EXACTLY these lines in this order:

**Headline:** [Plain prose, MAX 100 CHARACTERS, no markdown, no trailing period. This is the one-liner shown in the posted H2; if it exceeds 100 chars it will be cut and look bad. Pick the single most important takeaway — e.g. "Ships the partner auth flow cleanly, but token storage still leaks identity into query strings".]

**Verdict:** [1-2 sentences: what this PR does and whether it's ready to merge. Headline may repeat the gist; this line gives nuance.]

[severity line — compact inline, only non-zero counts, e.g.:]
🔴 4 P0 · 🟡 3 P1 · 💡 2 P2 · 64 files reviewed

**Top priority:** [The single most important root cause to fix first.]

**Fix order:** file1.ts → file2.ts → file3.ts
[One line. Arrow-separated. Dependency order.]

**Architecture:** [1 sentence — what's good, what to watch.]

Rules:
- Headline is REQUIRED. Count characters carefully — the 100-char limit is hard.
- Severity line: only include non-zero counts. Never show "0 suggestions" or "0 clean".
- Do NOT use a markdown table. Use the compact inline format shown above.
- If score >= 8, keep the verdict positive and brief. Omit fix order and top priority.
- If critical issues exist, Top priority and Fix order are required.
- If no critical issues, omit both.
- Group related findings by ROOT CAUSE, then surface the root cause in Top priority.
- Fix order: dependency order. If fixing file A changes the API file B uses, list A first.
- Do NOT list individual findings — those are inline.
- Use "we" not "you". Collaborative tone.
- Argus advises, it never gates merges. When the PR is not ready, say it "needs work" and name what would change the verdict — never "blocked", "rejected", "do not merge", or similar denial language.
- No greetings, no score, no link, no comment count — those are shown separately.`

// generateConversationalBrief calls the LLM to produce a natural-language summary of the review.
// Falls back to a deterministic brief on failure.
func (o *Orchestrator) generateConversationalBrief(ctx context.Context, run *PipelineRun, score int) string {
	// Build deterministic fallback (suppressed findings are never shown, so they
	// must not inflate the brief's counts)
	var criticals, warnings int
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.Suppressed {
				continue
			}
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
		MaxTokens:   synthesisBriefMaxTokens,
		Temperature: 0.7,
		// Brief needs a little thought to pick the right framing — "low"
		// gives quality without the TTFT tail of "medium".
		ReasoningEffort: llm.ReasoningLow,
		Stage:           "synthesis_brief",
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
		// Mid-stage signal: brief is ready, distinct from EventSynthesis which
		// fires at the very end of synthesize() after all sub-work completes.
		// fallback = true when the LLM returned empty; the caller falls back
		// to a deterministic template.
		trimmedLen := len(strings.TrimSpace(resp.Content))
		run.EventBus.Publish(run.ReviewID, EventBriefGenerated, map[string]any{
			"length":   trimmedLen,
			"fallback": trimmedLen == 0,
		})
	}

	brief := strings.TrimSpace(resp.Content)
	if brief == "" {
		return fallback
	}

	if !strings.HasPrefix(brief, "**") {
		if idx := strings.Index(brief, "**Verdict:"); idx > 0 {
			brief = strings.TrimSpace(brief[idx:])
		} else if idx := strings.Index(brief, "**"); idx > 0 {
			brief = strings.TrimSpace(brief[idx:])
		} else {
			firstSentences := extractFirstSentences(brief, 2)
			if firstSentences != "" {
				brief = fmt.Sprintf("**Verdict:** %s", firstSentences)
			} else {
				o.logger.Info("synthesis brief: no parseable content, using fallback")
				return fallback
			}
		}
	}

	return brief
}

func extractFirstSentences(text string, n int) string {
	sentences := strings.SplitAfterN(text, ". ", n+1)
	if len(sentences) > n {
		sentences = sentences[:n]
	}
	joined := strings.Join(sentences, "")
	joined = strings.TrimSpace(joined)
	for _, prefix := range []string{"The user ", "Looking at ", "Based on ", "I ", "Let me "} {
		if strings.HasPrefix(joined, prefix) {
			if idx := strings.Index(joined, ". "); idx > 0 {
				joined = strings.TrimSpace(joined[idx+1:])
			} else {
				return ""
			}
		}
	}
	if len(joined) < 20 {
		return ""
	}
	return joined
}

func buildSynthesisBriefPrompt(run *PipelineRun, score int) string {
	var sb strings.Builder
	safeTitle := sanitizeUserInput(util.Truncate(run.PREvent.PRTitle, 200, false))
	safeAuthor := sanitizeUserInput(util.Truncate(run.PREvent.PRAuthor, 100, false))
	sb.WriteString(fmt.Sprintf("PR #%d: \"%s\" by %s\n", run.PREvent.PRNumber, safeTitle, safeAuthor))
	sb.WriteString(fmt.Sprintf("Files reviewed: %d, Score: %d/10\n\n", len(run.Diff.Files), score))

	// Prefer the structured intent block — it's denser than a 300-char body slice and
	// carries non_goals / acceptance_criteria so the brief can be scope-aware. Fall
	// back to the raw body only when intent extraction produced nothing usable.
	if intent := run.PRIntent.RenderPrompt(); intent != "" {
		sb.WriteString(intent + "\n\n")
	} else if run.PREvent.PRBody != "" {
		sb.WriteString(wrapInDelimiters("pr_description", sanitizeUserInput(util.Truncate(run.PREvent.PRBody, 300, false))) + "\n\n")
	}

	// Per-file severity counts so the LLM can populate the heatmap table
	type fileSeverity struct {
		critical, warning, suggestion, praise int
	}
	perFile := make(map[string]*fileSeverity)
	var allFiles []string
	for _, fr := range run.FileReviews {
		fs, ok := perFile[fr.Path]
		if !ok {
			fs = &fileSeverity{}
			perFile[fr.Path] = fs
			allFiles = append(allFiles, fr.Path)
		}
		for _, c := range fr.Comments {
			if c.Suppressed {
				continue // dropped findings never reach the PR body brief/heatmap
			}
			switch c.Severity {
			case SeverityCritical:
				fs.critical++
			case SeverityWarning:
				fs.warning++
			case SeveritySuggestion:
				fs.suggestion++
			case SeverityPraise:
				fs.praise++
			}
		}
	}
	sort.Strings(allFiles)

	// Add files from diff that had no findings (clean files)
	reviewedFiles := make(map[string]bool)
	for _, f := range allFiles {
		reviewedFiles[f] = true
	}
	var cleanFiles []string
	for _, f := range run.Diff.Files {
		if !reviewedFiles[f.NewName] {
			cleanFiles = append(cleanFiles, f.NewName)
		}
	}

	sb.WriteString("Per-file findings:\n")
	for _, path := range allFiles {
		fs := perFile[path]
		sb.WriteString(fmt.Sprintf("- %s: %d critical, %d warning, %d suggestion\n", path, fs.critical, fs.warning, fs.suggestion))
	}
	if len(cleanFiles) > 0 {
		sb.WriteString(fmt.Sprintf("- Clean (0 findings): %s\n", strings.Join(cleanFiles, ", ")))
	}
	sb.WriteString("\nWrite a short verdict following the system prompt format. Populate the table with per-file data above.")
	return sb.String()
}

// topCategories returns the top N most frequent comment categories from FileReviews.
func topCategories(run *PipelineRun, n int) []string {
	counts := make(map[Category]int)
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.Suppressed {
				continue // dropped findings don't count toward the brief's key concerns
			}
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

	// Review contract gate: a second architecture pass earns nothing on
	// throwaway scripts, docs, or generated code.
	if run.Contract.SkipsPass2() {
		o.logger.Info("pass2 skipped by review contract",
			"change_class", run.Contract.ChangeClass, "pr", run.PREvent.PRNumber)
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

	// Cold file pass: re-review files with 0 comments and >50 changed lines
	// These are likely under-reviewed, not clean
	const maxColdFiles = 5
	const minLinesForCold = 50
	commentedFiles := make(map[string]bool)
	for _, fr := range run.FileReviews {
		if len(fr.Comments) > 0 {
			commentedFiles[fr.Path] = true
		}
	}
	var coldFiles []diff.FileDiff
	for _, f := range run.Diff.Files {
		if len(coldFiles) >= maxColdFiles {
			break
		}
		if !commentedFiles[f.NewName] && len(f.ChangedLines()) >= minLinesForCold {
			coldFiles = append(coldFiles, f)
		}
	}
	if len(coldFiles) > 0 {
		o.logger.Info("cold file pass starting", "files", len(coldFiles))
		coldCtx, coldCancel := context.WithTimeout(ctx, 60*time.Second)
		defer coldCancel()
		coldContents := make(map[string]string)
		for _, f := range coldFiles {
			content, fetchErr := o.ghClient.GetFileContent(coldCtx, run.PREvent.InstallationID, owner, repo, f.NewName, run.PREvent.HeadSHA)
			if fetchErr == nil {
				coldContents[f.NewName] = content
			}
		}
		for _, f := range coldFiles {
			if coldCtx.Err() != nil {
				o.logger.Warn("cold file pass timeout", "completed", len(coldFiles), "pr", run.PREvent.PRNumber)
				break
			}
			p := reviewParams{
				file:       f,
				action:     TriageDeep,
				specialist: SpecialistSecurity,
				systemBase: specialistPrompt(SpecialistSecurity, run.Prompts),
				deepReview: true,
			}
			rev, tok, err := o.reviewStage.reviewFile(coldCtx, run, p, coldContents, owner, repo, cfg, provider)
			if err != nil {
				o.logger.Warn("cold file review failed", "file", f.NewName, "error", err)
				continue
			}
			run.Tokens.Review = append(run.Tokens.Review, tok)
			run.Tokens.addToTotal(tok)
			for i := range rev.Comments {
				rev.Comments[i].Specialist = "cold_security"
			}
			if len(rev.Comments) > 0 {
				run.FileReviews = append(run.FileReviews, rev)
			}
		}
		o.logger.Info("cold file pass complete", "files_reviewed", len(coldFiles))
	}

	// Re-dedup after pass2 — pass2 findings often overlap with pass1
	beforeDedup := countComments(run)
	run.FileReviews = SmartDedup(run.FileReviews, 10, 0.7)
	afterDedup := countComments(run)
	if beforeDedup != afterDedup {
		o.logger.Info("pass2 dedup", "before", beforeDedup, "after", afterDedup, "removed", beforeDedup-afterDedup)
	}

	// Validate StartLine against diff — clear invalid start_line to prevent
	// GitHub 422 "start_line could not be resolved". Comments with invalid Line
	// are NOT dropped here; the post stage folds them into the review summary
	// so findings on affected (non-diff) lines are still surfaced to the user.
	validLines := make(map[string]map[int]bool)
	for _, f := range run.Diff.Files {
		validLines[f.NewName] = f.ValidCommentLines()
	}
	var nonDiffCount int
	for i := range run.FileReviews {
		fr := &run.FileReviews[i]
		fileValid := validLines[fr.Path]
		for j := range fr.Comments {
			c := &fr.Comments[j]
			if fileValid == nil || !fileValid[c.Line] {
				nonDiffCount++
			}
			if c.StartLine > 0 && (fileValid == nil || !fileValid[c.StartLine]) {
				c.StartLine = 0
			}
		}
	}
	if nonDiffCount > 0 {
		o.logger.Info("pass2 comments on non-diff lines (will fold into summary)", "count", nonDiffCount, "pr", run.PREvent.PRNumber)
	}
	return nil
}

func (o *Orchestrator) post(ctx context.Context, run *PipelineRun) error {
	// Final cancel guard: a Stop that landed after the last stage-boundary
	// cooperative check must still keep us from posting. Never post a review the
	// user cancelled. Returning context.Canceled routes Run through
	// handleCancelled (state → cancelled, no completion write). The guard lives
	// in ReviewLifecycle.ShouldAbortPost — the single home for this re-check.
	if o.lifecycle.ShouldAbortPost(ctx, run.ReviewID, "post: review status check failed") {
		o.logger.Info("post: review cancelled, skipping GitHub post", "review_id", run.ReviewID)
		return context.Canceled
	}

	// Guard: don't re-post if this review was already posted (stale recovery)
	var existingReviewID *int64
	_ = o.db.QueryRow(ctx, `SELECT github_review_id FROM reviews WHERE id = $1`, run.ReviewID).Scan(&existingReviewID)
	if existingReviewID != nil && *existingReviewID > 0 {
		o.logger.Warn("skipping post — review already posted", "review_id", run.ReviewID, "github_review_id", *existingReviewID)
		return nil
	}

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		return err
	}

	if run.Synthesis == nil {
		return fmt.Errorf("synthesis result is nil, cannot post review")
	}

	// Compose the GitHub submission — a pure render of the summary body and
	// inline comment list (severity-then-score ordering, 10-cap + overflow,
	// out-of-diff folding, blocking-file nit demotion, post-selection dedup,
	// token breakdown, glass-box footer). All rendering lives in composer.go;
	// post() only persists the result and ships it to GitHub. Duration is injected
	// (Compose reads no clock) so its glass-box footer is deterministic under test.
	submission := Compose(run, time.Since(run.CreatedAt), o.cfg.DashboardBaseURL, o.cfg.GitHubAppSlug)
	counts := submission.Counts

	// Rendering observability: one line replacing the three Info logs that lived
	// in the old inline block (inline cap, post-selection dedup, folded split).
	// Compose stays pure (no logger); post() reports its derived counts. Keys
	// mirror the old logs (total/cap/overflow, folded_important/folded_minor) so
	// existing grep habits still work.
	o.logger.Info("composed review submission",
		"total", counts.InlineCandidates,
		"cap", maxInlineComments,
		"overflow", counts.CapOverflow,
		"dedup_removed", counts.DedupRemoved,
		"inline", len(submission.GitHub.Comments),
		"folded_important", counts.FoldedImportant,
		"folded_minor", counts.FoldedMinor,
	)
	// Predecessor-compat: restore the historical "capping inline comments for
	// GitHub" alert string (retired when the three logs consolidated above) for
	// the one operationally-significant case operators grep — inline overflow.
	// Fires only when the cap actually dropped comments, matching the old
	// conditional log; a legacy-message *key* on the line above would smuggle a
	// message string into a data field and still not read as a message-level
	// grep, so we re-emit the real line instead. The historical count key was
	// "dropped" (#146 review: the consolidation renamed it "overflow" — restored
	// here). The old line's per-file keys "unique_files"/"max_per_file" are
	// intentionally NOT restored: the per-file cap they described no longer
	// exists (post-selection dedup replaced it), so re-emitting them would report
	// a mechanism that isn't running.
	if counts.CapOverflow > 0 {
		o.logger.Info("capping inline comments for GitHub",
			"total", counts.InlineCandidates, "cap", maxInlineComments, "dropped", counts.CapOverflow)
	}

	// Persist review data BEFORE posting to GitHub so a 502 doesn't lose results
	var tokenUsageJSON []byte
	if run.Tokens.Total.TotalTokens > 0 {
		if b, err := json.Marshal(&run.Tokens); err != nil {
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
	var truncatedFilesJSON []byte
	if len(run.TruncatedFiles) > 0 {
		truncatedFilesJSON, _ = json.Marshal(run.TruncatedFiles)
	}
	var contractJSON []byte
	if run.Contract != nil {
		// Resolve a still-pending contract (intent stage fast-exited) to the
		// production default before persisting, so the stored value matches the
		// treat-empty-as-production behavior consumers already assume.
		run.Contract.Finalize()
		if b, err := json.Marshal(run.Contract); err != nil {
			o.logger.Warn("failed to marshal review contract", "error", err)
		} else {
			contractJSON = b
		}
	}
	_, dbErr := o.db.Exec(ctx, `
		UPDATE reviews SET summary = $1, score = $2, token_usage = $3, file_count = $4,
		       deep_review = $5, persona = $6, is_incremental = $7, simulation_results = $8,
		       truncated_files = $9, brief = $10, review_contract = $11
		WHERE id = $12
	`, run.Synthesis.Summary, run.Synthesis.Score, tokenUsageJSON, len(run.FileReviews),
		run.DeepReview, persona, run.IsIncremental, simResultsJSON, truncatedFilesJSON,
		run.Synthesis.Brief, contractJSON, run.ReviewID)
	if dbErr != nil {
		o.logger.Error("pre-post DB update failed — review data at risk if PostReview also fails",
			"error", dbErr, "review_id", run.ReviewID)
	}

	// Persist comments to DB BEFORE posting to GitHub.
	// If PostReview fails (403 rate limit, 502, etc.), comments are still
	// visible on the dashboard. ghReviewID=0 means github_comment_id is nil
	// for now — backfilled after successful post.
	// Guard: skip if comments already persisted (retry after post failure).
	var existingComments int
	_ = o.db.QueryRow(ctx, `SELECT COUNT(*) FROM review_comments WHERE review_id = $1`, run.ReviewID).Scan(&existingComments)
	if existingComments == 0 {
		o.indexComments(ctx, run, 0, owner, repo)
	}
	o.indexConfirmedPatterns(ctx, run, owner, repo)

	// Pre-post memory sinks: pattern learning, convention extraction, file-memory
	// synthesis, and PR/architecture summary indexing. Run BEFORE PostReview so a
	// 403/502 there doesn't lose them. Detached from ctx so a post-review cancel
	// doesn't skip indexing; each sink is panic-isolated by RunAll (one exploding
	// indexer must not abort the others or the completion write).
	prePostCtx := context.WithoutCancel(ctx)
	o.indexer().RunAll(prePostCtx, run, owner, repo, "pre_post", []memorySink{
		{name: "autoLearnPatterns", enabled: func(r *PipelineRun) bool { return r.LearnPatterns }, run: o.autoLearnPatterns},
		{name: "learnPositivePatterns", enabled: func(r *PipelineRun) bool { return r.LearnPatterns }, run: func(ctx context.Context, r *PipelineRun, owner, repo string) {
			o.learnPositivePatterns(ctx, r, owner, repo)
		}},
		{name: "extractConventions", enabled: func(r *PipelineRun) bool { return r.LearnConventions }, run: o.extractConventions},
		{name: "synthesizeFileMemories", enabled: func(r *PipelineRun) bool { return r.FileSynthesis }, run: o.synthesizeFileMemories},
		{name: "indexPRSummary", run: o.indexPRSummary},
		{name: "indexArchitectureSummary", run: o.indexArchitectureSummary},
	})

	// Final cancel guard, immediately before the GitHub post: the pre-post
	// enrichment block above runs for seconds, and a cross-machine Stop landing
	// in that window must still prevent the post. (The check at the top of
	// post() only covers a cancel that arrived before enrichment ran.) Same
	// single-home guard as the entry check.
	if o.lifecycle.ShouldAbortPost(ctx, run.ReviewID, "post: pre-PostReview status check failed") {
		o.logger.Info("post: review cancelled before GitHub post, skipping", "review_id", run.ReviewID)
		return context.Canceled
	}

	ghReviewID, err := o.ghClient.PostReview(
		ctx,
		run.PREvent.InstallationID,
		owner, repo,
		run.PREvent.PRNumber,
		&submission.GitHub,
	)
	if err != nil {
		return fmt.Errorf("posting review: %w", err)
	}

	// Persist github_review_id UNCONDITIONALLY and immediately, separate from
	// the status compare-and-set below. If a cross-machine Stop lands between
	// here and the status write, that CAS no-ops (status already cancelled) and
	// would leave github_review_id NULL — then a later retry's already-posted
	// guard (keyed on github_review_id) wouldn't fire and we'd double-post to
	// GitHub. Recording the id right after the post closes that window. Best
	// effort: don't fail the review (it IS posted) if this write blips.
	if _, idErr := o.db.Exec(ctx,
		`UPDATE reviews SET github_review_id = $1 WHERE id = $2`,
		ghReviewID, run.ReviewID,
	); idErr != nil {
		o.logger.Error("post: failed to persist github_review_id after PostReview", "error", idErr, "review_id", run.ReviewID, "github_review_id", ghReviewID)
	}

	// comment.posted is fired after the atomic PostReview landed so failures
	// above produce review.failed instead. comment_count reflects what the
	// author will actually see on GitHub: inline comments posted via the
	// review submission. Folded-into-summary comments don't count — they're
	// rendered as part of a single "summary" comment, not per-thread.
	o.logger.InfoContext(ctx, "comment posted",
		slog.String("event", "comment.posted"),
		slog.String("review_id", run.ReviewID.String()),
		slog.String("repo", run.PREvent.RepoFullName),
		slog.Int("pr_number", run.PREvent.PRNumber),
		slog.Int("comment_count", len(submission.GitHub.Comments)),
		slog.String("trace_id", run.TraceID),
	)

	// Minimize the "review started" comment now that the full review is posted
	if run.StartedCommentNodeID != "" {
		if err := o.ghClient.MinimizeComment(ctx, run.PREvent.InstallationID, run.StartedCommentNodeID, "RESOLVED"); err != nil {
			o.logger.Warn("failed to minimize started comment", "error", err)
		}
	}

	// Mark completed + store github_review_id + clear any stale error.
	// The error column gets set by RecoverStaleReviews ("review timed out —
	// server restarted") if the recovery job ran before we finished posting.
	// Clearing it prevents completed reviews from showing a ghost timeout error.
	// Conditional on status='in_progress': a Stop that raced past the pre-post
	// guards (marking the review cancelled) must not be clobbered back to
	// completed. The GitHub review is already posted, so we don't roll it back;
	// we just don't overwrite a terminal status another writer set.
	tag, err := o.db.Exec(ctx, `
		UPDATE reviews
		SET status = 'completed', github_review_id = $1, completed_at = NOW(), error = NULL
		WHERE id = $2 AND status = 'in_progress'
	`, ghReviewID, run.ReviewID)
	if err != nil {
		return fmt.Errorf("updating review record: %w", err)
	}
	if tag.RowsAffected() == 0 {
		o.logger.Warn("post: completion write skipped — review no longer in_progress", "review_id", run.ReviewID)
	}

	// Persist linked_pr_refs BEFORE the EventReviewCompleted publish so the
	// sibling reverse-lookup in crosspr_stage.OnReviewCompleted sees this
	// review's refs the instant a sibling fires. Non-fatal — an indexing
	// miss here just means a linked sibling won't be refreshed on its next
	// completion; the next push to either PR re-triggers.
	o.persistReviewLinkedPRRefs(ctx, run)
	// Persist linked_issue_refs alongside linked_pr_refs. Same
	// ordering contract (BEFORE publish) so FindSharedLinkedIssues sees
	// up-to-date data the instant a sibling's EventReviewCompleted fires.
	o.persistReviewLinkedIssueRefs(ctx, run)

	// Published only after the DB commit for status='completed' returned nil.
	// Moving this inside the transaction would leak events on rollback —
	// see cross-PR stage handler in crosspr_stage.go.
	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventReviewCompleted, ReviewCompletedPayload{
			ReviewID:       run.ReviewID,
			RepoID:         run.DBRepoID,
			PRNumber:       run.PREvent.PRNumber,
			InstallationID: run.PREvent.InstallationID,
		})
	}

	// "comments" is the total FileReview count (post-dedup/scoring). Split it into
	// what actually reached the author: inline posts vs folded-into-summary. A high
	// folded count on a review with low inline can signal line-number drift in the
	// reviewer LLM or a diff the author didn't touch the lines we flagged on.
	o.logger.Info("[posted] review", "github_review_id", ghReviewID, "pr", run.PREvent.PRNumber,
		"comments", countComments(run),
		"inline", len(submission.GitHub.Comments),
		"folded_important", counts.FoldedImportant,
		"folded_minor", counts.FoldedMinor,
		"files", len(run.FileReviews), "score", run.Synthesis.Score,
		"deep_review", run.DeepReview, "duration_ms", time.Since(run.CreatedAt).Milliseconds())

	// Live-stream the GitHub POST completion distinct from "completed" — the
	// latter fires only after all post-post work (memory indexing, backfill,
	// pattern learning). This event marks the moment the author's PR gets the
	// inline comments visible on GitHub.
	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventPostedToGitHub, map[string]any{
			"github_review_id": ghReviewID,
			"inline":           len(submission.GitHub.Comments),
			"folded":           counts.FoldedImportant + counts.FoldedMinor,
		})
	}

	// Backfill github_comment_ids now that we have the ghReviewID
	o.backfillGitHubCommentIDs(ctx, run, ghReviewID, owner, repo)

	// ThreadRegistry (#162): bind each just-posted finding to its GraphQL
	// review-thread node id. Runs AFTER the backfill above so the github_comment_id
	// join key is present; authoritative one-shot hydrate off the fresh review.
	o.hydrateThreadNodeIDs(ctx, run, owner, repo)

	// Pattern learning, conventions, file synthesis, and PR summary now run
	// BEFORE PostReview (see above). Only architecture graph + PR enrichment
	// remain post-review; same panic-isolated RunAll loop.
	postCtx := context.WithoutCancel(ctx)
	o.indexer().RunAll(postCtx, run, owner, repo, "post_review", []memorySink{
		{name: "extractArchitectureGraph", enabled: func(r *PipelineRun) bool { return r.ArchitectureGraph }, run: o.extractArchitectureGraph},
		{name: "enrichPRDescription", panicMsg: "enrichPRDescription panic", enabled: func(r *PipelineRun) bool { return r.PREnrichment }, run: o.enrichPRDescription},
	})

	// Persist final token usage including post-review ops
	if run.Tokens.Total.TotalTokens > 0 {
		if b, err := json.Marshal(&run.Tokens); err == nil {
			if _, err := o.db.Exec(ctx, `UPDATE reviews SET token_usage = $1 WHERE id = $2`, b, run.ReviewID); err != nil {
				o.logger.Warn("failed to persist post-review tokens", "error", err)
			}
		}
	}

	// Collect changed file paths once for scenario outdating
	changedPaths := make([]string, 0, len(run.Diff.Files))
	for _, f := range run.Diff.Files {
		changedPaths = append(changedPaths, f.NewName)
	}

	if len(run.Synthesis.SimulationResults) > 0 && run.Indexer != nil {
		for _, result := range run.Synthesis.SimulationResults {
			if !result.Passes && result.Confidence >= 0.5 {
				matches := scenarioSearch(ctx, run.Indexer, o.logger, repo, result.Scenario, "", 1)
				if len(matches) > 0 {
					passed := matches[0].Similarity >= run.Thresholds.ScenarioTrigger
					o.logger.Info("threshold_check",
						"name", "scenario_trigger",
						"value", matches[0].Similarity,
						"threshold", run.Thresholds.ScenarioTrigger,
						"passed", passed)
					if passed {
						if err := o.st.IncrementScenarioTriggerCount(ctx, matches[0].ID); err != nil {
							o.logger.Warn("incrementing scenario trigger count", "error", err)
						}
					}
				}
			}
		}
	}

	// Mark scenarios touching changed files as outdated
	if err := o.st.MarkScenarioOutdated(ctx, run.DBRepoID, changedPaths); err != nil {
		o.logger.Warn("marking scenarios outdated", "error", err, "pr", run.PREvent.PRNumber)
	}

	// Collect decision traces — persisted to Postgres only. Postgres
	// decision_traces is the source of truth; Supermemory trace writes were
	// retired (observational, never read back into reviews, pure write noise).
	traceSeeds := CollectReviewTraces(run)
	var traceFails int
	for _, seed := range traceSeeds {
		if err := o.st.CreateTrace(ctx, run.DBRepoID, seed.FilePath, seed.SymbolName, seed.TraceType, seed.Content, seed.Severity, seed.ReviewID, seed.PRNumber, seed.Metadata); err != nil {
			traceFails++
		}
	}
	if traceFails > 0 {
		o.logger.Warn("some decision traces failed to persist", "failed", traceFails, "total", len(traceSeeds))
	}

	// Auto-learn scenarios from critical/warning findings (gated by feature flag).
	if run.ScenarioMemory {
		scenarioSeeds := ExtractScenariosFromReview(run)
		StoreScenarioSeeds(ctx, o.st, run.Indexer, owner, repo, run.DBInstallationID, &run.DBRepoID, run.Thresholds.ScenarioDedupe, scenarioSeeds)
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

You will also be given diagram instructions at the end of the prompt. Follow them exactly.

Respond with JSON only:
{
  "missing_points": ["Adds batch processing with configurable concurrency and retry logic"],
  "diagrams": [
    {"type": "sequence", "title": "Request Flow", "mermaid": "sequenceDiagram\n  Client->>API: fetch()\n  API->>Config: loadConfig()"}
  ]
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
		MaxTokens:   1500,
		Temperature: 0.3,
		JSONMode:    true,
		Stage:       "pr_enrichment",
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

	type diagramResult struct {
		Type    string `json:"type"`
		Title   string `json:"title"`
		Mermaid string `json:"mermaid"`
	}
	var result struct {
		MissingPoints []string        `json:"missing_points"`
		Diagrams      []diagramResult `json:"diagrams"`
		Diagram       string          `json:"diagram"`
		DiagramTitle  string          `json:"diagram_title"`
	}
	cleaned := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		o.logger.Warn("enrichPRDescription: failed to parse LLM response", "error", err, "response_prefix", util.Truncate(resp.Content, 200, true))
		return
	}

	// Backwards compat: migrate legacy single diagram to diagrams array
	if result.Diagram != "" && len(result.Diagrams) == 0 {
		title := result.DiagramTitle
		if title == "" {
			title = "Architecture"
		}
		result.Diagrams = []diagramResult{{Type: "dependency", Title: title, Mermaid: result.Diagram}}
	}

	if len(result.MissingPoints) == 0 && len(result.Diagrams) == 0 {
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

	var validDiagrams []diagramResult
	for _, d := range result.Diagrams {
		if d.Mermaid == "" || !isValidMermaid(d.Mermaid) {
			o.logger.Warn("skipping invalid diagram", "type", d.Type, "title", d.Title)
			continue
		}
		validDiagrams = append(validDiagrams, d)
	}
	if len(validDiagrams) > 0 {
		diagramsJSON, marshalErr := json.Marshal(validDiagrams)
		if marshalErr != nil {
			o.logger.Warn("failed to marshal diagrams", "error", marshalErr)
		} else if _, dbErr := o.db.Exec(ctx, `UPDATE reviews SET diagrams = $1 WHERE id = $2`,
			diagramsJSON, run.ReviewID); dbErr != nil {
			o.logger.Warn("failed to save diagrams", "error", dbErr)
		}
		if _, dbErr := o.db.Exec(ctx, `UPDATE reviews SET diagram = $1, diagram_title = $2 WHERE id = $3`,
			validDiagrams[0].Mermaid, validDiagrams[0].Title, run.ReviewID); dbErr != nil {
			o.logger.Warn("failed to save legacy diagram", "error", dbErr)
		}
	}
	for _, d := range validDiagrams {
		title := sanitizeUserInput(d.Title)
		if title == "" {
			title = capitalizeCategory(d.Type)
		}
		section.WriteString("<details>\n")
		section.WriteString(fmt.Sprintf("<summary>%s</summary>\n\n", title))
		section.WriteString("```mermaid\n" + d.Mermaid + "\n```\n")
		section.WriteString("</details>\n\n")
	}
	section.WriteString(fmt.Sprintf("<sub>Auto-enriched by [Argus](%s)</sub>\n", o.cfg.DashboardBaseURL))
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
	diagramLower := strings.ToLower(diagram)
	validKeywords := []string{"sequencediagram", "graph ", "flowchart", "classdiagram", "erdiagram", "gantt", "pie", "statediagram", "journey", "gitgraph"}
	hasKeyword := false
	for _, kw := range validKeywords {
		if strings.Contains(diagramLower, kw) {
			hasKeyword = true
			break
		}
	}
	if !hasKeyword {
		return false
	}
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

	// Diagram instructions (deterministic selection, LLM generates content)
	specs := selectDiagramTypes(run)
	if len(specs) > 0 {
		sb.WriteString("\n### Diagram instructions:\n")
		sb.WriteString("Generate the following diagrams in the `diagrams` array. Each must be valid Mermaid syntax.\n\n")
		for _, s := range specs {
			sb.WriteString(fmt.Sprintf("**%s** (max %d nodes):\n%s\n\n", s.Title, s.MaxNodes, s.Instruction))
		}
	} else {
		sb.WriteString("\n### Diagram instructions:\nNo diagrams needed — return empty `diagrams` array.\n")
	}

	return sb.String()
}

// indexConfirmedPatterns saves high-confidence comments as confirmed repo patterns in Supermemory.
// When scoring is available, uses score ≥80 (deep) or ≥90 (non-deep). When skipped, falls back to critical+warning severity.
func (o *Orchestrator) indexConfirmedPatterns(ctx context.Context, run *PipelineRun, owner, repo string) {
	if run.Indexer == nil {
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
			// Never re-learn a finding the developer already dismissed. reviews is
			// the pre-enrich AllFileReviews snapshot, so check the key map rather
			// than c.Suppressed (which the snapshot's copy lacks).
			if run.isSuppressed(fr.Path, c.Line, c.Body) {
				continue
			}
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
			resp, err := run.Indexer.IndexPattern(ctx, repo, memory.PatternMemory{
				Content:  content,
				CustomID: customID,
				Source:   "scoring_confirmed",
				Score:    c.Score,
				PRNumber: run.PREvent.PRNumber,
				Category: string(c.Category),
			})
			if err != nil {
				// Non-fatal, but the DB row below lands with a NULL supermemory_id —
				// log the deterministic customID (never model-generated content,
				// which can quote secrets) so silent write-failures are visible.
				o.logger.Warn("indexing confirmed pattern", "error", err, "file", fr.Path,
					"custom_id", customID)
			}
			// Also persist to local DB so the patterns dashboard stays current
			var smID *string
			if resp != nil && resp.ID != "" {
				smID = &resp.ID
			}
			src := "scoring_confirmed"
			cat := string(c.Category)
			prNum := run.PREvent.PRNumber
			if _, dbErr := o.st.CreatePattern(ctx, run.DBInstallationID, &run.DBRepoID, content, smID, strPtrOrNil("argus:confirmed"), &src, &cat, &prNum, strPtrOrNil(customID)); dbErr != nil {
				o.logger.Warn("persisting confirmed pattern to DB", "error", dbErr, "file", fr.Path)
			}
		}
	}
	slog.Info("indexConfirmedPatterns", "indexed", indexed, "scoring_skipped", run.ScoringSkipped)
	if indexed > 0 {
		publishMemoryIndexed(run, "patterns", true, indexed)
	}
}

// learnPositivePatterns indexes praise comments as positive patterns in Supermemory.
// These patterns suppress future false positives on similar good code.
func (o *Orchestrator) learnPositivePatterns(ctx context.Context, run *PipelineRun, owner, repo string) int {
	if run.Indexer == nil || !run.LearnPatterns {
		return 0
	}

	reviews := run.FileReviews
	if len(run.AllFileReviews) > 0 {
		reviews = run.AllFileReviews
	}

	var indexed int
	for _, fr := range reviews {
		for _, c := range fr.Comments {
			// Praise is never a dismissal target in practice, but gate anyway for
			// consistency with the other learning paths (reads pre-enrich snapshot).
			if run.isSuppressed(fr.Path, c.Line, c.Body) {
				continue
			}
			if c.Severity != SeverityPraise {
				continue
			}
			// Route praise through IndexFeedbackSignal so the doc lands with
			// type=feedback, polarity=positive, action=confirmed metadata —
			// pure-prose content (c.Body), structured fields in metadata.
			if err := run.Indexer.IndexFeedbackSignal(ctx, owner, repo, memory.FeedbackMemory{
				FilePath:     fr.Path,
				Category:     string(c.Category),
				OriginalBody: c.Body,
				Action:       "confirmed",
				PRNumber:     run.PREvent.PRNumber,
			}); err != nil {
				o.logger.Warn("positive pattern indexing failed", "error", err)
			} else {
				indexed++
			}
		}
	}

	if indexed > 0 {
		o.logger.Info("pattern_learning_event",
			"action", "positive_patterns",
			"count", indexed,
			"repo", run.PREvent.RepoFullName,
			"pr", run.PREvent.PRNumber,
		)
	}
	if indexed > 0 {
		publishMemoryIndexed(run, "patterns_praise", true, indexed)
	}
	return indexed
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
	if run.Indexer == nil {
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
			// Skip dismissal-dropped findings (key map, since reviews is the
			// pre-enrich snapshot without the Suppressed flag).
			if run.isSuppressed(fr.Path, c.Line, c.Body) {
				continue
			}
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
		o.logger.Info("pattern_learning_event",
			"action", "skipped",
			"qualifying", len(highConf),
			"min_required", minRequired,
			"scoring_skipped", run.ScoringSkipped,
			"repo", run.PREvent.RepoFullName,
			"pr", run.PREvent.PRNumber,
		)
		return
	}
	o.logger.Info("pattern_learning_event",
		"action", "extracting",
		"qualifying", len(highConf),
		"scoring_skipped", run.ScoringSkipped,
		"repo", run.PREvent.RepoFullName,
		"pr", run.PREvent.PRNumber,
	)

	cfg, provider, err := o.resolveReviewProvider(ctx, run)
	if err != nil {
		o.logger.Warn("auto-learn skipped", "error", err)
		return
	}

	prompt := fmt.Sprintf(`From these high-confidence review findings on %s, extract 0-3 reusable patterns SPECIFIC to this codebase (not generic best practices). Each pattern should help catch similar issues in future PRs.

Findings:
%s

Return JSON array: [{"pattern": "<concrete reusable pattern text>", "category": "bug|security|architecture|regression"}]
The "pattern" value must be the actual pattern text, NOT the word "description". Return [] if no repo-specific patterns emerge. JSON array only.`, run.PREvent.RepoFullName, strings.Join(highConf, "\n"))

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      "You extract reusable code review patterns from review findings. Be specific to this codebase.",
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   500,
		Temperature: 0.3,
		JSONMode:    true,
		Stage:       "pattern_learning",
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
		if p.Pattern == "" || p.Pattern == "description" {
			continue
		}
		customID := memory.PatternCustomID(owner, repo, "learned", p.Pattern)
		smResp, err := run.Indexer.IndexPattern(ctx, repo, memory.PatternMemory{
			Content:  p.Pattern,
			CustomID: customID,
			Source:   "auto_learn",
			PRNumber: run.PREvent.PRNumber,
			Category: p.Category,
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
		if _, dbErr := o.st.CreatePattern(ctx, run.DBInstallationID, &run.DBRepoID, p.Pattern, smID, strPtrOrNil("argus:auto_learn"), &src, cat, &prNum, strPtrOrNil(customID)); dbErr != nil {
			o.logger.Warn("persisting auto-learned pattern", "error", dbErr)
		}

		// Also store as org-level if pattern is generic (doesn't reference repo-specific file paths)
		if isGenericPattern(p.Pattern, run.Diff) {
			orgCustomID := memory.PatternCustomID(owner, "", "org_learned", p.Pattern)
			var orgSmID *string
			orgResp, orgErr := run.Indexer.IndexSharedPattern(ctx, memory.PatternMemory{
				Content:  p.Pattern,
				CustomID: orgCustomID,
				Source:   "auto_learn",
				PRNumber: run.PREvent.PRNumber,
				Category: p.Category,
				Extra:    map[string]string{"repo": run.PREvent.RepoFullName},
			})
			if orgErr != nil {
				o.logger.Warn("indexing org pattern", "error", orgErr)
			} else if orgResp != nil {
				orgSmID = &orgResp.ID
			}
			if _, dbErr := o.st.CreatePattern(ctx, run.DBInstallationID, nil, p.Pattern, orgSmID, strPtrOrNil("argus:auto_learn"), &src, cat, &prNum, strPtrOrNil(orgCustomID)); dbErr != nil {
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
		// Kind is "patterns" (not "patterns_praise") — autoLearnPatterns extracts
		// from reviewer findings, not praise comments. patterns_praise is the
		// praise-derived flow in learnPositivePatterns.
		publishMemoryIndexed(run, "patterns", true, len(patterns))
	}
}

// extractConventions analyzes the PR diff to identify code style conventions and
// architectural patterns used in the codebase. Unlike autoLearnPatterns (which extracts
// patterns from review comments), this function learns from the code itself —
// capturing what the team writes, not what the reviewer flags.
func (o *Orchestrator) extractConventions(ctx context.Context, run *PipelineRun, owner, repo string) {
	if run.Indexer == nil || run.Diff == nil || len(run.Diff.Files) == 0 {
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
		Stage:       "conventions",
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
		smResp, err := run.Indexer.IndexPattern(ctx, repo, memory.PatternMemory{
			Content:  content,
			CustomID: customID,
			Source:   "convention_extraction",
			PRNumber: run.PREvent.PRNumber,
			Category: c.Category,
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
		if _, dbErr := o.st.CreatePattern(ctx, run.DBInstallationID, &run.DBRepoID, content, smID, strPtrOrNil("argus:convention"), &src, cat, &prNum, strPtrOrNil(customID)); dbErr != nil {
			o.logger.Warn("persisting convention pattern", "error", dbErr)
		}
	}

	if len(conventions) > 0 {
		o.logger.Info("extracted conventions", "count", len(conventions), "repo", run.PREvent.RepoFullName)
		publishMemoryIndexed(run, "conventions", true, len(conventions))
	}
}

// synthesizeFileMemories condenses all review comments per file into a single curated memory document.
// Fires for files with signal: 1+ comment scored 60+, or any comment 80+.
// When scoring was skipped, any file with 1+ comment qualifies.
func (o *Orchestrator) synthesizeFileMemories(ctx context.Context, run *PipelineRun, owner, repo string) {
	if run.Indexer == nil {
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
		// Drop dismissal-suppressed findings before they can seed a file-synthesis
		// memory (pre-enrich snapshot ⇒ key map, not the in-place Suppressed flag).
		kept := make([]FileComment, 0, len(fr.Comments))
		for _, c := range fr.Comments {
			if run.isSuppressed(fr.Path, c.Line, c.Body) {
				continue
			}
			kept = append(kept, c)
		}
		if len(kept) == 0 {
			continue
		}
		if run.ScoringSkipped {
			// Scoring unavailable — qualify any file with 1+ comments
			qualifying = append(qualifying, fileComments{path: fr.Path, comments: kept})
			continue
		}
		count60 := 0
		has80 := false
		for _, c := range kept {
			if c.Score >= 60 {
				count60++
			}
			if c.Score >= 80 {
				has80 = true
			}
		}
		if count60 >= 1 || has80 {
			qualifying = append(qualifying, fileComments{path: fr.Path, comments: kept})
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

	// No wrapper timeout. The parent `prePostCtx` at orchestrator.go:2029 is
	// `context.WithoutCancel(ctx)` — its Done channel is nil, so a ctx.Err()
	// loop-break here would be dead code. The real upper bound is the HTTP
	// client timeout (llmClientTimeout in chat.go, 600s) applied once per LLM
	// call inside provider.Complete. Worst case: a hung Azure endpoint costs
	// 600s per file before we give up and move on. Acceptable for post-posting
	// work; revisit if logs show this actually pegs us.
	safeTitle := sanitizeUserInput(util.Truncate(run.PREvent.PRTitle, 200, false))
	safeAuthor := sanitizeUserInput(util.Truncate(run.PREvent.PRAuthor, 100, false))

	var succeeded, failed int
	for _, fc := range qualifying {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("File: %s\nPR #%d: \"%s\" by %s\n\nComments:\n",
			fc.path, run.PREvent.PRNumber, safeTitle, safeAuthor))
		for _, c := range fc.comments {
			sb.WriteString(fmt.Sprintf("[%s|%s] L%d (%s, score:%d) — %s\n",
				c.Severity, c.Category, c.Line, c.Specialist, c.Score, c.Body))
		}

		resp, err := provider.Complete(ctx, llm.CompletionRequest{
			Model:       cfg.Model,
			System:      synthesisSystem,
			Messages:    []llm.Message{{Role: "user", Content: sb.String()}},
			MaxTokens:   fileSynthesisMaxTokens,
			Temperature: 0.3,
			Stage:       "file_synthesis",
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
		_, err = run.Indexer.IndexPattern(ctx, repo, memory.PatternMemory{
			Content:  resp.Content,
			CustomID: customID,
			Source:   "synthesis",
			PRNumber: run.PREvent.PRNumber,
			FilePath: fc.path,
		})
		if err != nil {
			o.logger.Warn("indexing file synthesis", "error", err, "file", fc.path)
			failed++
			continue
		}
		succeeded++
	}

	o.logger.Info("synthesized file memories", "succeeded", succeeded, "failed", failed, "repo", run.PREvent.RepoFullName)
	if succeeded+failed > 0 {
		publishMemoryIndexed(run, "file_synthesis", failed == 0, succeeded)
	}
}

// indexPRSummary stores a lightweight PR summary in Supermemory for cross-PR context.
// No LLM call — built from existing synthesis output.
func (o *Orchestrator) indexPRSummary(ctx context.Context, run *PipelineRun, owner, repo string) {
	if run.Indexer == nil || run.Synthesis == nil {
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
	_, err := run.Indexer.IndexPattern(ctx, repo, memory.PatternMemory{
		Content:  content,
		CustomID: customID,
		Source:   "pr_summary",
		PRNumber: run.PREvent.PRNumber,
		PRAuthor: run.PREvent.PRAuthor,
	})
	if err != nil {
		o.logger.Warn("indexing PR summary", "error", err)
	}
	publishMemoryIndexed(run, "pr_summary", err == nil, 1)
}

// indexArchitectureSummary indexes the repo's top choke points into Supermemory so
// future reviews can surface architectural risk context. Idempotent (uses customID per repo).
// Skips repos with fewer than 3 choke points.
func (o *Orchestrator) indexArchitectureSummary(ctx context.Context, run *PipelineRun, owner, repo string) {
	if run.Indexer == nil {
		return
	}

	chokeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rows, err := o.st.GetTopChokePoints(chokeCtx, run.DBRepoID, 10)
	if err != nil {
		o.logger.Warn("indexing arch summary: query", "error", err)
		return
	}
	if len(rows) < 3 {
		return // not enough signal yet
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Architecture Summary for %s/%s:\n\n", owner, repo))
	sb.WriteString("Choke points (files with high fan-in — many other files depend on them):\n")
	for _, r := range rows {
		sb.WriteString(fmt.Sprintf("- %s (fan_in=%d)\n", r.FilePath, r.FanIn))
	}
	sb.WriteString("\nWhen reviewing changes to these files, apply extra scrutiny: defects propagate to all dependent modules.")

	// Sanitize both segments individually so characters Supermemory rejects
	// on customId (`/`, `(`, `)`, `[`, `]`, `.`, etc.) can't sneak in via
	// unusual owner/repo names. The literal format string uses `--` between
	// them — not `/` — so the customId stays in the allowed char set.
	customID := fmt.Sprintf("arch-summary:%s--%s", memory.CustomIDSanitize(owner), memory.CustomIDSanitize(repo))
	_, err = run.Indexer.IndexPattern(chokeCtx, repo, memory.PatternMemory{
		Content:  sb.String(),
		CustomID: customID,
		Source:   "arch_summary",
		Extra:    map[string]string{"choke_points": fmt.Sprintf("%d", len(rows))},
	})
	if err != nil {
		o.logger.Warn("indexing arch summary", "error", err)
		publishMemoryIndexed(run, "arch_summary", false, 0)
		return
	}
	o.logger.Info("indexed architecture summary", "owner", owner, "repo", repo, "choke_points", len(rows))
	publishMemoryIndexed(run, "arch_summary", true, len(rows))
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
		Stage:       "arch_graph",
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
	publishMemoryIndexed(run, "arch_graph", true, len(nodeIDs))
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

func (o *Orchestrator) dedupStage(ctx context.Context, run *PipelineRun) error {
	if !run.DeepReview {
		// Free-tier / non-deep incremental re-reviews still get structural
		// prior-duplicate suppression — ungated from DeepReview so every
		// re-review, not just Pro deep ones, stops re-posting reworded
		// duplicates of prior comments. SmartDedup (cross-specialist) and the
		// pre-scoring AllFileReviews snapshot remain deep-only: single-pass
		// reviews have no cross-specialist duplicates, and scoring takes the
		// AllFileReviews snapshot for these tiers.
		if run.IsIncremental && len(run.PriorComments) > 0 {
			if dropped := dropPriorDuplicates(run); dropped > 0 {
				o.logger.Info("[dedup] dropped prior-duplicates (non-deep)", "count", dropped, "pr", run.PREvent.PRNumber)
			}
		}
		return nil
	}
	before := countComments(run)
	run.AllFileReviews = make([]FileReview, len(run.FileReviews))
	copy(run.AllFileReviews, run.FileReviews)

	run.FileReviews = SmartDedup(run.FileReviews, 10, 0.7)

	// Incremental reviews: drop findings that are near-duplicates of comments
	// already posted on a prior review of this PR. The review prompt asks the
	// LLM to respect prior comments but it re-rediscovers the same bug with
	// different wording; this is the enforcement layer.
	if run.IsIncremental && len(run.PriorComments) > 0 {
		droppedPrior := dropPriorDuplicates(run)
		if droppedPrior > 0 {
			o.logger.Info("[dedup] dropped prior-duplicates", "count", droppedPrior, "pr", run.PREvent.PRNumber)
		}
	}

	after := countComments(run)
	o.logger.Info("[dedup] OK", "before", before, "after", after, "removed", before-after, "pr", run.PREvent.PRNumber)
	return nil
}

// dropPriorDuplicates mutates run.FileReviews in place, removing FileComments
// that structurally match a comment already posted on a prior review of this
// PR — same file, same Category (case-insensitive), line within ±10. Returns
// the number of dropped comments.
//
// The match uses the shared Matcher (see finding_match.go) with the
// prior-dedup policy {Proximity: 10, UseCategory: true} so the rule can no
// longer drift from the auto-resolve / cross-PR sites.
//
// Rationale: the LLM reliably rewords findings across reviews, so string
// similarity is unreliable; structural match (file + line + category) is the
// only dedup we can trust. Proximity=10 is intentionally wide because the
// developer's push may have shifted line numbers.
func dropPriorDuplicates(run *PipelineRun) int {
	if run == nil {
		return 0
	}
	matcher := Matcher{Proximity: 10, UseCategory: true}
	var dropped int
	for i := range run.FileReviews {
		fr := &run.FileReviews[i]
		priors, ok := run.PriorComments[fr.Path]
		if !ok || len(priors) == 0 {
			continue
		}
		kept := fr.Comments[:0]
		for _, c := range fr.Comments {
			if hasPriorMatch(matcher, fr.Path, c, priors) {
				dropped++
				continue
			}
			kept = append(kept, c)
		}
		fr.Comments = kept
	}
	return dropped
}

// hasPriorMatch reports whether any prior comment on path is a structural
// duplicate of c under matcher. path is the FileReview path both anchors sit on
// (run.PriorComments is keyed by it), so the Matcher's path check is a
// trivially-true guard here — the load-bearing predicate is line proximity +
// category.
func hasPriorMatch(matcher Matcher, path string, c FileComment, priors []PriorComment) bool {
	newAnchor := Anchor{Path: path, Line: c.Line, Category: string(c.Category)}
	for _, p := range priors {
		if matcher.Matches(newAnchor, Anchor{Path: path, Line: p.Line, Category: p.Category}) {
			return true
		}
	}
	return false
}

// validateStage enriches deduplicated findings with blast radius and simulation data.
// Runs blast radius + simulation in parallel on the clean finding set.
func (o *Orchestrator) validateStage(ctx context.Context, run *PipelineRun) error {
	start := time.Now()

	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		o.logger.Warn("[validate] bad repo name, skipping", "error", err, "pr", run.PREvent.PRNumber)
		return nil
	}

	changedPaths := diffFilePaths(run.Diff)

	// Run SAST, blast radius, and simulation in parallel (deep-review only),
	// plus issue acceptance + cross-PR checks (always, gated by their own flags).
	var blastImpacts []BlastRadiusImpact
	var simResults []SimulationResult
	var sastFindings []sast.Finding
	var wg sync.WaitGroup

	if run.DeepReview {
		o.logger.Info("[validate] starting", "findings", countComments(run), "pr", run.PREvent.PRNumber)

		// SAST analysis
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("[validate] SAST goroutine panic", "recover", r, "pr", run.PREvent.PRNumber)
					emitPipelinePanicEvent(ctx, o.logger, "validate_sast", r, run.TraceID)
				}
			}()
			if len(changedPaths) > 50 {
				o.logger.Info("[validate] SAST skipped — too many files", "files", len(changedPaths), "pr", run.PREvent.PRNumber)
				return
			}
			// Determine dominant language from changed files
			lang := dominantLanguage(changedPaths)
			if lang == "" {
				return
			}
			// Collect file contents — use FullContent if available, otherwise fetch via API
			files := make(map[string]string)
			for _, f := range run.Diff.Files {
				if f.FullContent != "" {
					files[f.NewName] = f.FullContent
				} else if f.NewName != "" {
					content, err := o.ghClient.GetFileContent(ctx, run.PREvent.InstallationID, owner, repo, f.NewName, run.PREvent.HeadSHA)
					if err != nil {
						o.logger.Warn("[validate] SAST: failed to fetch file", "file", f.NewName, "error", err, "pr", run.PREvent.PRNumber)
					} else if content != "" {
						files[f.NewName] = content
					}
				}
			}
			if len(files) == 0 {
				return
			}
			runners := sast.DefaultRunners()
			sastCtx, sastCancel := context.WithTimeout(ctx, 30*time.Second)
			defer sastCancel()
			results, err := sast.RunAll(sastCtx, runners, lang, files)
			if err != nil {
				o.logger.Warn("[validate] SAST failed", "error", err, "pr", run.PREvent.PRNumber)
				return
			}
			sastFindings = results
			o.logger.Info("[validate] SAST done", "findings", len(results), "lang", lang, "pr", run.PREvent.PRNumber)
		}()

		// Blast Radius
		wg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("[validate] blast radius goroutine panic", "recover", r, "pr", run.PREvent.PRNumber)
					emitPipelinePanicEvent(ctx, o.logger, "blast_radius", r, run.TraceID)
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
					emitPipelinePanicEvent(ctx, o.logger, "simulation", r, run.TraceID)
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
				simScenarios[i] = SimScenario{ID: s.ID, Description: s.Description, Severity: s.Severity, Source: s.Source, Files: s.Files}
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
	} else {
		o.logger.Info("[validate] deep-review workers skipped — not deep review", "pr", run.PREvent.PRNumber)
	}

	// Issue acceptance + cross-PR always run (gated by their own feature flags, not deep review).

	// Issue acceptance — judges PR diff against linked issue criteria.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				o.logger.Error("[validate] issue acceptance goroutine panic", "recover", r, "pr", run.PREvent.PRNumber)
				emitPipelinePanicEvent(ctx, o.logger, "issue_acceptance", r, run.TraceID)
			}
		}()
		o.runIssueAcceptanceWorker(ctx, run)
	}()

	// Cross-repo PR compatibility moved to runCrossPRStage (crosspr_stage.go):
	// it fires asynchronously after the primary review commits, so the primary
	// review's latency stays minimal and late-arriving sibling PRs can
	// trigger a refresh without re-running the full pipeline. Handler
	// subscription to EventReviewCompleted is wired in NewOrchestrator.

	wg.Wait()

	// Store SAST findings on PipelineRun for scoring corroboration
	if len(sastFindings) > 0 {
		run.SastFindings = make(map[string][]SastFinding)
		for _, f := range sastFindings {
			run.SastFindings[f.File] = append(run.SastFindings[f.File], SastFinding{
				File: f.File, Line: f.Line, Rule: f.Rule, Message: f.Message, Severity: f.Severity,
			})
		}
		// Mark comments corroborated by SAST
		for i := range run.FileReviews {
			fr := &run.FileReviews[i]
			fileSast, ok := run.SastFindings[fr.Path]
			if !ok {
				continue
			}
			for j := range fr.Comments {
				c := &fr.Comments[j]
				for _, sf := range fileSast {
					if util.IntAbs(c.Line-sf.Line) <= 3 {
						c.SastCorroborated = true
						break
					}
				}
			}
		}
		o.logger.Info("[validate] SAST corroboration", "corroborated", countCorroborated(run), "pr", run.PREvent.PRNumber)
	}

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

// indexer returns the PostReviewIndexer bound to this orchestrator, used by
// post() to run its pre-post and post-review memory sink clusters under one
// panic-isolation loop.
func (o *Orchestrator) indexer() *PostReviewIndexer {
	return &PostReviewIndexer{o: o}
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
			var matchedPatternID *int64
			if c.MatchedPatternID > 0 {
				id := c.MatchedPatternID
				matchedPatternID = &id
			}
			enforcedRule := strPtrOrNil(c.EnforcedRuleContent)
			suppressedReason := strPtrOrNil(c.SuppressedReason)
			state := store.FindingStatePosted
			if c.Suppressed {
				state = store.FindingStateSuppressed
			}

			formattedBody := formatCommentBody(c)
			// Suppressed (dropped) comments are still persisted — flagged via
			// suppressed_reason + state='suppressed' and with github_comment_id
			// nil (never posted) — so the dashboard keeps the full record while
			// the PR stays clean.
			if err := o.st.CreateReviewComment(ctx, run.ReviewID, fr.Path, startLine, &line, &side, formattedBody, &sev, &cat, specialist, snippet, confidenceScore, ghCommentID, matchedPatternID, matchedPatternScore, enforcedRule, c.IsNewFinding, suppressedReason, state); err != nil {
				o.logger.Error("persisting review comment", "error", err, "file", fr.Path)
			}

		}
	}

	// Batch index all comments to Supermemory in a single API call. The write
	// floor keeps low-signal findings out of the reviews container: critical/
	// warning always, suggestions only when scored >= reviewSuggestionScoreFloor,
	// praise never. (DB persistence above is unfiltered — the dashboard shows
	// everything; only memory retrieval is gated.)
	if run.Indexer != nil {
		var batch []memory.ReviewMemory
		for _, fr := range run.FileReviews {
			for _, c := range fr.Comments {
				if c.Suppressed {
					continue // never re-index a finding we just suppressed as dismissed
				}
				if !shouldIndexReviewMemory(c.Severity, c.Score, run.ScoringSkipped) {
					continue
				}
				batch = append(batch, memory.ReviewMemory{
					ReviewID: run.ReviewID.String(),
					PRNumber: run.PREvent.PRNumber,
					FilePath: fr.Path,
					Body:     c.Body,
					Severity: string(c.Severity),
					Category: string(c.Category),
				})
			}
		}
		if err := run.Indexer.IndexReviewCommentsBatch(ctx, owner, repo, batch); err != nil {
			o.logger.Error("batch indexing review comments", "error", err, "count", len(batch))
		}
	}
}

// backfillGitHubCommentIDs fetches the actual GitHub comment IDs from the
// posted review and updates the pre-persisted review_comments rows.
func (o *Orchestrator) backfillGitHubCommentIDs(ctx context.Context, run *PipelineRun, ghReviewID int64, owner, repo string) {
	if ghReviewID == 0 {
		return
	}
	ghComments, err := o.ghClient.ListReviewComments(ctx, run.PREvent.InstallationID, owner, repo, run.PREvent.PRNumber, ghReviewID)
	if err != nil {
		o.logger.Error("listing review comments for backfill", "error", err)
		return
	}
	updated := 0
	for _, gc := range ghComments {
		line := gc.GetLine()
		if line == 0 {
			line = gc.GetPosition()
		}
		ghID := gc.GetID()
		_, err := o.db.Exec(ctx, `
			UPDATE review_comments
			SET github_comment_id = $1
			WHERE review_id = $2 AND file_path = $3 AND end_line = $4 AND github_comment_id IS NULL
		`, ghID, run.ReviewID, gc.GetPath(), line)
		if err != nil {
			o.logger.Error("backfilling github_comment_id", "error", err, "file", gc.GetPath(), "line", line)
		} else {
			updated++
		}
	}
	if updated > 0 {
		o.logger.Info("backfilled github comment IDs", "count", updated, "review_id", run.ReviewID)
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

// publishMemoryIndexed emits an EventMemoryIndexed for the given Supermemory
// upsert. `kind` is one of the 7 closed values (patterns, patterns_praise,
// conventions, file_synthesis, pr_summary, arch_summary, arch_graph). `success`
// is false when the upsert returned an error — the UI renders failures with a
// distinct style so operators can spot indexing outages in live streams.
func publishMemoryIndexed(run *PipelineRun, kind string, success bool, count int) {
	if run == nil || run.EventBus == nil {
		return
	}
	run.EventBus.Publish(run.ReviewID, EventMemoryIndexed, map[string]any{
		"kind":    kind,
		"success": success,
		"count":   count,
	})
}

// pluralize returns noun + "s" when n != 1, matching the casual-English rule
// Argus uses for user-facing counts ("1 file", "2 files"). Irregular plurals
// are not handled — add a switch here if one appears in a new code path.
func pluralize(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// formatTokens renders a token count with a k/M suffix for readability.
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// priorityLabel maps severity to P0/P1/P2/P3 priority labels.
func priorityLabel(s Severity) string {
	switch s {
	case SeverityCritical:
		return "P0"
	case SeverityWarning:
		return "P1"
	case SeveritySuggestion:
		return "P2"
	default:
		return ""
	}
}

// confidenceScore returns a 1-10 confidence score.
// Uses LLM judge score when available (Score > 0), otherwise derives from severity.
func confidenceScore(c FileComment) int {
	if c.Score > 0 {
		s := c.Score / 10
		if s < 1 {
			s = 1
		}
		if s > 10 {
			s = 10
		}
		return s
	}
	// Fallback: derive from severity when scoring failed
	switch c.Severity {
	case SeverityCritical:
		return 9
	case SeverityWarning:
		return 7
	case SeveritySuggestion:
		return 4
	case SeverityPraise:
		return 10
	default:
		return 5
	}
}

// renderMemoryTag produces a one-line italic attribution appended to the review
// comment body when Argus matched the finding against a memory document (past
// pattern / team convention / repo rule / prior-review similarity). Returns
// "" when no memory match is present — callers skip appending on empty.
//
// Multi-match rendering collapses all hit kinds into a single sentence joined
// with " and ". Pattern hits carry attribution (PR#, author, age); other kinds
// are shorter because they don't have a single source PR.
//
// Example outputs:
//
//	_— Matches a repo rule._
//	_— Matches a prior fix in PR #927 (@jordan, 2 months ago)._
//	_— Matches a repo rule and a prior fix in PR #927._
//	_— Matches the team's style convention._
func renderMemoryTag(c FileComment) string {
	var clauses []string

	// Rule is authoritative — author-written, not inferred. List first when present.
	if c.EnforcedRuleContent != "" {
		clauses = append(clauses, "a repo rule")
	}

	switch c.MatchedPatternKind {
	case "pattern":
		var tail string
		if c.MatchedPatternPR > 0 {
			tail = fmt.Sprintf(" in PR #%d", c.MatchedPatternPR)
			if c.MatchedPatternAuthor != "" {
				tail += fmt.Sprintf(" (@%s", c.MatchedPatternAuthor)
				if age := humanAge(c.MatchedPatternAgeDays); age != "" {
					tail += ", " + age
				}
				tail += ")"
			} else if age := humanAge(c.MatchedPatternAgeDays); age != "" {
				tail += " (" + age + ")"
			}
		}
		clauses = append(clauses, "a prior fix"+tail)
	case "convention":
		phrase := "the team's convention"
		if cat := string(c.Category); cat != "" {
			phrase = "the team's " + strings.ReplaceAll(cat, "_", " ") + " convention"
		}
		clauses = append(clauses, phrase)
	case "similarity":
		clauses = append(clauses, "a similar prior finding")
	}

	if len(clauses) == 0 {
		return ""
	}

	var joined string
	switch len(clauses) {
	case 1:
		joined = clauses[0]
	case 2:
		joined = clauses[0] + " and " + clauses[1]
	default:
		joined = strings.Join(clauses[:len(clauses)-1], ", ") + ", and " + clauses[len(clauses)-1]
	}
	return "_— Matches " + joined + "._"
}

// humanAge formats a day count as a human phrase suitable for inline prose.
// Returns "" for 0/negative days so the renderer can drop the age clause
// entirely rather than print "0 days ago".
func humanAge(days int) string {
	switch {
	case days <= 0:
		return ""
	case days == 1:
		return "1 day ago"
	case days < 30:
		return fmt.Sprintf("%d days ago", days)
	case days < 60:
		return "1 month ago"
	case days < 365:
		return fmt.Sprintf("%d months ago", days/30)
	case days < 730:
		return "1 year ago"
	default:
		return fmt.Sprintf("%d years ago", days/365)
	}
}

func severityEmoji(s Severity) string {
	switch s {
	case SeverityCritical:
		return "\U0001F534" // red circle
	case SeverityWarning:
		return "\U0001F7E1" // yellow circle
	case SeveritySuggestion:
		return "\U0001F4A1" // lightbulb
	case SeverityPraise:
		return "\u2705" // green check
	default:
		return "\U0001F4A1"
	}
}

func capitalizeCategory(cat string) string {
	if len(cat) == 0 {
		return cat
	}
	// Normalize: "error_handling" → "Error handling", "type_design" → "Type design"
	cat = strings.ReplaceAll(cat, "_", " ")
	return strings.ToUpper(cat[:1]) + cat[1:]
}

// commentTitleSentenceRe matches sentence boundaries: a period, exclamation,
// or question mark followed by whitespace and an uppercase letter. This dodges
// the common mid-word period false positives (e.g., "v1.2", "e.g.", URL dots,
// abbreviations like "Dr.", "i.e.") that plain `. ` split tripped on.
var commentTitleSentenceRe = regexp.MustCompile(`[.!?]\s+[A-Z]`)

// commentTitle returns the one-line headline for the comment.
// The LLM is prompted to produce `what` as a single short sentence, but when
// it emits multiple we take only the first. The sentence boundary is detected
// via a regex that requires an uppercase letter after whitespace, which avoids
// breaking on mid-word periods. Falls back to a 300-char rune-boundary-safe
// truncation (via util.Truncate) when no sentence boundary is found.
func commentTitle(c FileComment) string {
	src := c.What
	if src == "" {
		src = c.Body
	}
	if idx := commentTitleSentenceRe.FindStringIndex(src); idx != nil && idx[0] > 0 && idx[0] < 280 {
		// idx[0] points at the terminal punctuation; include it.
		return src[:idx[0]+1]
	}
	return util.Truncate(src, 300, true)
}

type diagramSpec struct {
	Type        string // "sequence", "dataflow", "dependency"
	Title       string
	Instruction string // LLM instruction for this diagram type
	MaxNodes    int
}

// selectDiagramTypes picks up to 2 diagram types based on PR characteristics.
// Priority: sequence > dataflow > dependency.
func selectDiagramTypes(run *PipelineRun) []diagramSpec {
	var specs []diagramSpec

	fileCount := 0
	if run.Diff != nil {
		fileCount = len(run.Diff.Files)
	}

	// Sequence diagram: 3+ changed files
	if fileCount >= 3 {
		specs = append(specs, diagramSpec{
			Type:  "sequence",
			Title: "Call Sequence",
			Instruction: "Generate a Mermaid sequenceDiagram showing which changed files/modules call each other. " +
				"Annotate any participants involved in bugs with ⚠️. Max 12 participants.",
			MaxNodes: 12,
		})
	}

	// Data flow diagram: security findings, sensitive file paths, or injection-related content
	dataflow := false
	sensitivePaths := []string{
		"auth", "token", "session", "fetch", "api", "login",
		"oauth", "password", "credential", "validate", "input", "config",
	}
	contentKeywords := []string{
		"injection", "xss", "ssrf", "redirect", "sanitiz", "escap",
	}

	for _, fr := range run.FileReviews {
		if dataflow {
			break
		}
		for _, c := range fr.Comments {
			if strings.ToLower(string(c.Category)) == "security" {
				dataflow = true
				break
			}
			lower := strings.ToLower(c.What + " " + c.Body)
			for _, kw := range contentKeywords {
				if strings.Contains(lower, kw) {
					dataflow = true
					break
				}
			}
			if dataflow {
				break
			}
		}
	}

	if !dataflow && run.Diff != nil {
		for _, f := range run.Diff.Files {
			lowerPath := strings.ToLower(f.NewName)
			for _, sp := range sensitivePaths {
				if strings.Contains(lowerPath, sp) {
					dataflow = true
					break
				}
			}
			if dataflow {
				break
			}
		}
	}

	if dataflow {
		specs = append(specs, diagramSpec{
			Type:  "dataflow",
			Title: "Data Flow",
			Instruction: "Generate a Mermaid flowchart TD tracing untrusted input through the system. " +
				"Mark tainted paths with ⚠️. Max 10 nodes.",
			MaxNodes: 10,
		})
	}

	// Dependency graph: 10+ changed files
	if fileCount >= 10 {
		specs = append(specs, diagramSpec{
			Type:  "dependency",
			Title: "Dependency Graph",
			Instruction: "Generate a Mermaid graph LR showing import relationships between changed files. " +
				"Max 12 nodes.",
			MaxNodes: 12,
		})
	}

	// Cap at 2 (priority order already correct: sequence > dataflow > dependency)
	if len(specs) > 2 {
		specs = specs[:2]
	}
	if len(specs) == 0 {
		return nil
	}
	return specs
}

// rebalanceSeverity downgrades lowest-confidence critical findings to warning
// when >50% of all comments are critical. Prevents everything-is-a-blocker.
func rebalanceSeverity(reviews []FileReview) {
	type commentRef struct {
		fileIdx, commentIdx int
		score               int
	}
	var total int
	var criticals []commentRef
	for fi, fr := range reviews {
		for ci, c := range fr.Comments {
			if c.Suppressed {
				continue // dropped findings aren't shown, so they can't skew the critical ratio or consume slots
			}
			total++
			if c.Severity == SeverityCritical {
				criticals = append(criticals, commentRef{fi, ci, c.Score})
			}
		}
	}
	if total == 0 || float64(len(criticals))/float64(total) <= 0.5 {
		return
	}
	sort.Slice(criticals, func(i, j int) bool {
		return criticals[i].score < criticals[j].score
	})
	target := total / 2
	excess := len(criticals) - target
	for i := 0; i < excess; i++ {
		ref := criticals[i]
		reviews[ref.fileIdx].Comments[ref.commentIdx].Severity = SeverityWarning
	}
	slog.Info("rebalanced severity", "total", total, "criticals_before", len(criticals), "downgraded", excess)
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// countComments counts postable findings. Dismissal-suppressed (dropped)
// comments are excluded so they never inflate the summary pill, the synthesis
// count line, or any post-enrichment log — they are persisted but never shown.
func countComments(run *PipelineRun) int {
	total := 0
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.Suppressed {
				continue
			}
			total++
		}
	}
	return total
}

func calculateScore(run *PipelineRun) int {
	var criticals, warnings, suggestions int
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.Suppressed {
				continue // dropped findings are not shown, so they don't move the score
			}
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

// dominantLanguage returns the most common language among changed file paths.
// On ties, uses a fixed priority: go > typescript > javascript > python.
func dominantLanguage(paths []string) string {
	counts := map[string]int{}
	for _, p := range paths {
		ext := strings.ToLower(path.Ext(p))
		switch ext {
		case ".go":
			counts["go"]++
		case ".ts", ".tsx":
			counts["typescript"]++
		case ".js", ".jsx", ".mjs", ".cjs":
			counts["javascript"]++
		case ".py":
			counts["python"]++
		case ".java":
			counts["java"]++
		case ".rs":
			counts["rust"]++
		case ".cs":
			counts["csharp"]++
		case ".rb":
			counts["ruby"]++
		case ".kt", ".kts":
			counts["kotlin"]++
		case ".swift":
			counts["swift"]++
		case ".c", ".h":
			counts["c"]++
		case ".cpp", ".cc", ".cxx", ".hpp":
			counts["cpp"]++
		case ".php":
			counts["php"]++
		case ".scala":
			counts["scala"]++
		case ".dart":
			counts["dart"]++
		}
	}
	// Fixed priority order for deterministic tie-breaking
	priority := []string{"go", "typescript", "javascript", "python", "java", "rust", "csharp", "ruby", "kotlin", "swift", "c", "cpp", "php", "scala", "dart"}
	var best string
	var bestCount int
	for _, lang := range priority {
		if c := counts[lang]; c > bestCount {
			best = lang
			bestCount = c
		}
	}
	return best
}

func jaccardSimilarity(a, b map[string]bool) float64 {
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// countCorroborated counts findings marked as SAST-corroborated.
func countCorroborated(run *PipelineRun) int {
	n := 0
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.SastCorroborated {
				n++
			}
		}
	}
	return n
}
