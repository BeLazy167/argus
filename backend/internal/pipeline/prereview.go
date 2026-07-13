// Package pipeline — prereview.go de-inlines HandlePREvent's four pre-review
// context "islands" (SAST hints, architecture context, linked issues/PRs +
// feature flags, and author intent) into standalone non-fatal enrichers.
//
// Each enricher reads its inputs from and writes its outputs onto the shared
// *PipelineRun, so both the fresh webhook path (HandlePREvent) and the
// retry-rebuild paths (retryFromReviewRow, RetryReview via buildRetryRun)
// assemble IDENTICAL context by calling o.enrichPreReview. Before this, retries
// skipped these islands entirely — most importantly the intent stage — so a
// retried review persisted a contract with an empty change class and lost
// intent-aware review context.
//
// Every enricher is best-effort: a failure logs at Warn/Error and leaves the
// corresponding run field at its zero value so the pipeline proceeds with
// degraded (never absent) context. Each takes a narrow, consumer-declared
// dependency interface (the crosspr_stage_deps.go idiom) so it can be
// unit-tested against an in-memory fake with no live GitHub client, store, or
// LLM.
package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/sast"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/BeLazy167/argus/backend/pkg/diff"
)

const (
	// prereviewSASTFileCap skips the SAST pre-pass for diffs larger than this
	// many files — the per-file content fetches don't fit the timeout budget.
	prereviewSASTFileCap = 50
	// prereviewSASTTimeout bounds the SAST pre-pass, including file fetches.
	prereviewSASTTimeout = 30 * time.Second
	// prereviewArchTimeout bounds the two bulk architecture-context queries.
	prereviewArchTimeout = 5 * time.Second
	// prereviewLinkTimeout bounds the closing-issues fetch and the feature-flag
	// read for the links+flags island.
	prereviewLinkTimeout = 10 * time.Second
)

// sastFileFetcher fetches file content at a commit for the SAST pre-pass.
// *ghpkg.Client satisfies it verbatim.
type sastFileFetcher interface {
	GetFileContent(ctx context.Context, installationID int64, owner, repo, path, ref string) (string, error)
}

// archContextReader reads the graph-derived architecture signals for a repo.
// *store.Store satisfies it verbatim.
type archContextReader interface {
	ListArchFileEdges(ctx context.Context, repoID int64) ([]db.ListArchFileEdgesRow, error)
	ListArchBugDensity(ctx context.Context, repoID int64) ([]db.ListArchBugDensityRow, error)
}

// linkEnricherDeps bundles the two boundaries the links+flags island touches:
// GitHub's closingIssuesReferences resolver and the per-installation
// feature_flags reader. Production composes *ghpkg.Client + *store.Store via
// defaultLinkDeps; a test fake implements both in a few lines.
type linkEnricherDeps interface {
	GetClosingIssues(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]ghpkg.ClosingIssueRef, error)
	GetInstallationFeatureFlags(ctx context.Context, installationID int64) (json.RawMessage, error)
}

// intentExecutor extracts author intent onto the run. *IntentExtractionStage
// satisfies it; a nil stage disables extraction (see intentExecutorFor).
type intentExecutor interface {
	Execute(ctx context.Context, run *PipelineRun) error
}

// attachSAST runs the SAST pre-pass over the changed files and attaches any
// findings to run.SastFindings, keyed by file, so the review stage can surface
// them as hints. Non-fatal: on any failure run.SastFindings stays nil.
//
// Skips entirely for diffs over prereviewSASTFileCap files or when no dominant
// language is detected. File content comes from the diff's FullContent when
// present, else a GitHub fetch via dep.
func attachSAST(ctx context.Context, run *PipelineRun, dep sastFileFetcher, logger *slog.Logger) {
	if run == nil || run.Diff == nil || len(run.Diff.Files) > prereviewSASTFileCap {
		return
	}
	lang := dominantLanguage(diffFilePaths(run.Diff))
	if lang == "" {
		return
	}
	owner, repo, err := splitRepoFullName(run.PREvent.RepoFullName)
	if err != nil {
		logger.Warn("[pre-review] SAST: invalid repo name, skipping", "error", err, "pr", run.PREvent.PRNumber)
		return
	}
	sastCtx, cancel := context.WithTimeout(ctx, prereviewSASTTimeout)
	defer cancel()

	files := make(map[string]string)
	for _, f := range run.Diff.Files {
		if f.FullContent != "" {
			files[f.NewName] = f.FullContent
		} else if f.NewName != "" {
			content, fetchErr := dep.GetFileContent(sastCtx, run.PREvent.InstallationID, owner, repo, f.NewName, run.PREvent.HeadSHA)
			if fetchErr != nil {
				logger.Warn("[pre-review] SAST: failed to fetch file", "file", f.NewName, "error", fetchErr, "pr", run.PREvent.PRNumber)
			} else if content != "" {
				files[f.NewName] = content
			}
		}
	}
	if len(files) == 0 {
		return
	}
	findings, sastErr := sast.RunAll(sastCtx, sast.DefaultRunners(), lang, files)
	if sastErr != nil {
		logger.Warn("[pre-review] SAST failed", "error", sastErr, "pr", run.PREvent.PRNumber)
		return
	}
	if len(findings) == 0 {
		return
	}
	run.SastFindings = make(map[string][]SastFinding)
	for _, f := range findings {
		run.SastFindings[f.File] = append(run.SastFindings[f.File], SastFinding{
			File: f.File, Line: f.Line, Rule: f.Rule, Message: f.Message, Severity: f.Severity,
		})
	}
	logger.Info("[pre-review] SAST hints ready", "findings", len(findings), "lang", lang, "pr", run.PREvent.PRNumber)
}

// attachArchContext fetches architecture context (choke points, bug hotspots)
// for the changed files and attaches the high-risk ones to run.ArchContext so
// the LLM knows which files are risky before reviewing. Non-fatal — two bulk
// queries (edges + bug density); if both fail run.ArchContext stays nil.
func attachArchContext(ctx context.Context, run *PipelineRun, dep archContextReader, logger *slog.Logger) {
	if run == nil {
		return
	}
	var files []diff.FileDiff
	if run.Diff != nil {
		files = run.Diff.Files
	}
	// No files means nothing to annotate — skip both repo-wide bulk queries
	// (degenerate retry inputs tolerate a nil diff).
	if len(files) == 0 {
		return
	}
	archCtx, cancel := context.WithTimeout(ctx, prereviewArchTimeout)
	defer cancel()

	edges, edgeErr := dep.ListArchFileEdges(archCtx, run.DBRepoID)
	if edgeErr != nil {
		logger.Warn("[pre-review] arch edges query failed", "error", edgeErr, "pr", run.PREvent.PRNumber)
	}
	density, densErr := dep.ListArchBugDensity(archCtx, run.DBRepoID)
	if densErr != nil {
		logger.Warn("[pre-review] arch bug density query failed", "error", densErr, "pr", run.PREvent.PRNumber)
	}
	if edgeErr != nil && densErr != nil {
		return
	}

	fanInByFile := make(map[string]int, len(edges))
	for _, e := range edges {
		fanInByFile[e.TargetPath]++
	}
	bugsByFile := make(map[string]int, len(density))
	for _, d := range density {
		bugsByFile[d.FilePath] = d.Bugs
	}

	archMap := make(map[string]ArchContextEntry, len(files))
	processed := 0
	for _, f := range files {
		if archCtx.Err() != nil {
			logger.Warn("[pre-review] arch context budget exceeded, partial data", "processed", processed, "total", len(files), "pr", run.PREvent.PRNumber)
			break
		}
		if f.NewName == "" {
			continue
		}
		processed++
		fanIn := fanInByFile[f.NewName]
		bugs := bugsByFile[f.NewName]
		if fanIn >= ArchChokePointFanIn || bugs >= ArchHotspotBugCount {
			archMap[f.NewName] = ArchContextEntry{FanIn: fanIn, BugCount: bugs}
		}
	}
	if len(archMap) > 0 {
		run.ArchContext = archMap
		logger.Info("[pre-review] arch context ready", "high_risk_files", len(archMap), "pr", run.PREvent.PRNumber)
	}
}

// attachLinks detects linked issues + cross-PRs from the PR body and GitHub's
// closingIssuesReferences, and loads the per-installation feature flags.
// Non-fatal — sets run.LinkedIssues, run.LinkedPRs, run.FeatureFlags (the last
// always non-nil: loadFeatureFlags falls back to DefaultFeatureFlags).
//
// The closing-issues fetch and the feature-flag read share one bounded context
// held open until the enricher returns (deferred cancel). The prior inline
// version cancelled the link context before the feature-flag read, which ran on
// a cancelled context and silently always fell back to defaults; the deferred
// cancel here fixes that so configured flags actually take effect.
func attachLinks(ctx context.Context, run *PipelineRun, dep linkEnricherDeps, logger *slog.Logger) {
	if run == nil {
		return
	}
	linkCtx, cancel := context.WithTimeout(ctx, prereviewLinkTimeout)
	defer cancel()

	var primary []IssueLink
	if owner, repo, splitErr := splitRepoFullName(run.PREvent.RepoFullName); splitErr == nil {
		closing, err := dep.GetClosingIssues(linkCtx, run.PREvent.InstallationID, owner, repo, run.PREvent.PRNumber)
		if err != nil {
			logger.Warn("[pre-review] closing issues fetch failed", "pr", run.PREvent.PRNumber, "error", err)
		} else {
			primary = make([]IssueLink, 0, len(closing))
			for _, c := range closing {
				primary = append(primary, IssueLink{
					Owner:      c.Owner,
					Repo:       c.Repo,
					Number:     c.Number,
					URL:        c.URL,
					Title:      c.Title,
					Body:       c.Body,
					Accessible: true,
				})
			}
		}
	}

	fallback := ExtractLinkedIssues(run.PREvent.PRBody, run.PREvent.RepoFullName)
	run.LinkedIssues = MergeIssueLinks(primary, fallback)
	run.LinkedPRs = ExtractLinkedPRs(run.PREvent.PRBody, run.PREvent.RepoFullName, run.PREvent.PRNumber, maxLinkedPRsFromEnv())

	if len(run.LinkedIssues) > 0 || len(run.LinkedPRs) > 0 {
		logger.Info("[pre-review] links detected", "issues", len(run.LinkedIssues), "prs", len(run.LinkedPRs), "pr", run.PREvent.PRNumber)
	}

	// Load per-installation feature flags (issue acceptance + cross-PR toggles).
	// Defaults: issue_acceptance on, cross_pr_checks off, max_linked_prs=5.
	run.FeatureFlags = loadFeatureFlags(linkCtx, dep, run.DBInstallationID)
}

// attachIntent extracts the author's stated motivation into run.PRIntent and,
// when the deterministic contract pass was silent, resolves the contract's
// change class from the extracted intent. Non-fatal — the stage always attaches
// a Source="empty" PRIntent on failure, and a nil (unwired) stage is skipped
// with a warning.
//
// Execute is documented to always return nil; a non-nil error indicates a
// programmer error (contract drift) and is logged loudly rather than swallowed.
func attachIntent(ctx context.Context, run *PipelineRun, stage intentExecutor, logger *slog.Logger) {
	if run == nil {
		return
	}
	if stage == nil {
		logger.Warn("[pre-review] intent stage not wired; skipping extraction", "pr", run.PREvent.PRNumber)
		return
	}
	if err := stage.Execute(ctx, run); err != nil {
		logger.Error("[pre-review] intent extraction returned unexpected error", "error", err, "pr", run.PREvent.PRNumber)
	}
}

// maxLinkedPRsFromEnv reads the ARGUS_MAX_LINKED_PRS override, clamped to
// (0,20]. Defaults to 5 when unset or out of range.
func maxLinkedPRsFromEnv() int {
	maxLinkedPRs := 5
	if v := os.Getenv("ARGUS_MAX_LINKED_PRS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20 {
			maxLinkedPRs = n
		}
	}
	return maxLinkedPRs
}

// defaultLinkDeps composes the concrete GitHub client and store into the
// linkEnricherDeps interface. Pure delegation — no business logic here.
type defaultLinkDeps struct {
	gh *ghpkg.Client
	st *store.Store
}

func (d defaultLinkDeps) GetClosingIssues(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]ghpkg.ClosingIssueRef, error) {
	return d.gh.GetClosingIssues(ctx, installationID, owner, repo, prNumber)
}

func (d defaultLinkDeps) GetInstallationFeatureFlags(ctx context.Context, installationID int64) (json.RawMessage, error) {
	// A nil store must degrade, not panic: wrapping o.st in this struct defeats
	// loadFeatureFlags' nil-interface guard (the typed-nil hazard
	// featureFlagReaderFor exists for), so the defusal lives here instead —
	// the error routes loadFeatureFlags to DefaultFeatureFlags.
	if d.st == nil {
		return nil, errors.New("feature flags: store unwired")
	}
	return d.st.GetInstallationFeatureFlags(ctx, installationID)
}

// intentExecutorFor adapts a possibly-nil *IntentExtractionStage to the
// intentExecutor interface, returning a nil INTERFACE (not a typed nil) when
// the stage is unwired so attachIntent's nil guard fires — mirrors
// featureFlagReaderFor's typed-nil defusal.
func intentExecutorFor(stage *IntentExtractionStage) intentExecutor {
	if stage == nil {
		return nil
	}
	return stage
}

// enrichPreReview runs the four pre-review context enrichers in sequence over
// run: SAST hints, architecture context, linked issues/PRs + feature flags, and
// author intent. Each is best-effort and independent; a failure in one leaves
// its run field zero-valued and does not affect the others.
//
// Shared by the fresh webhook path (HandlePREvent) and the retry-rebuild paths
// (retryFromReviewRow, RetryReview) so a retried review assembles the SAME
// intent-aware context — closing the gap where retries skipped intent and
// persisted contracts with an empty change class.
func (o *Orchestrator) enrichPreReview(ctx context.Context, run *PipelineRun) {
	if run == nil {
		return
	}
	attachSAST(ctx, run, o.ghClient, o.logger)
	attachArchContext(ctx, run, o.st, o.logger)
	attachLinks(ctx, run, defaultLinkDeps{gh: o.ghClient, st: o.st}, o.logger)
	attachIntent(ctx, run, intentExecutorFor(o.intentStage), o.logger)
}
