package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/BeLazy167/argus/backend/internal/util"
)

// scenarioSearch retrieves scenario matches (type=scenario) for query in the
// repo container and shapes them into []ScenarioSearchResult (scenario-id parse
// + dedup). Non-fatal: a search error degrades to nil via memory.BestEffort so
// scenario dedup/trigger just proceed as if nothing matched. severity="" leaves
// the search severity-agnostic. Retrieval is threshold-free (the caller applies
// the dedupe/trigger threshold to the returned similarities).
func scenarioSearch(ctx context.Context, indexer memory.Indexer, logger *slog.Logger, repo, query, severity string, limit int) []memory.ScenarioSearchResult {
	var filters []memory.FilterCondition
	if severity != "" {
		filters = append(filters, memory.FilterCondition{Key: "severity", Value: severity})
	}
	matches := memory.BestEffort(logger, "scenario", memory.RepoTagNew(repo), len(query),
		func() ([]memory.PatternMatch, error) {
			return indexer.Search(ctx, memory.MemoryQuery{
				Query:   query,
				Repo:    repo,
				Scope:   memory.ScopeRepo,
				Type:    memory.TypeScenario,
				Filters: filters,
				Limit:   limit,
				Rerank:  true,
			})
		})
	return memory.ScenarioResults(matches, limit)
}

// ScenarioSeed is an extracted scenario candidate from a completed review.
type ScenarioSeed struct {
	Description string
	Source      string
	SourceRef   string
	Files       []string
	Severity    string
}

// ExtractScenariosFromReview generates scenario seeds from significant review findings.
// Call after a review completes; persist seeds via store.CreateScenario.
// ExtractScenariosFromReview creates one scenario per critical/warning finding.
// Each scenario captures a specific issue — multiple unrelated issues in the same
// file become separate scenarios so none are lost.
func ExtractScenariosFromReview(run *PipelineRun) []ScenarioSeed {
	var seeds []ScenarioSeed
	for _, fr := range run.FileReviews {
		for _, c := range fr.Comments {
			if c.Severity != SeverityCritical && c.Severity != SeverityWarning {
				continue
			}
			what := c.What
			if what == "" {
				what = c.Body
			}
			desc := fmt.Sprintf("%s: %s", fr.Path, util.Truncate(what, 200, true))
			seeds = append(seeds, ScenarioSeed{
				Description: desc,
				Source:      "review",
				SourceRef:   run.ReviewID.String(),
				Files:       []string{fr.Path},
				Severity:    scenarioSeverity(c.Severity),
			})
		}
	}
	return seeds
}

// scenarioSeverity maps review severities to scenario severities.
func scenarioSeverity(s Severity) string {
	switch s {
	case SeverityCritical:
		return "critical"
	case SeverityWarning:
		return "high"
	case SeveritySuggestion:
		return "medium"
	default:
		return "low"
	}
}

// StoreScenarioSeeds persists extracted scenario seeds to the database.
// Wire this into the orchestrator after review completion:
//
//	seeds := pipeline.ExtractScenariosFromReview(run)
//	pipeline.StoreScenarioSeeds(ctx, st, run.DBInstallationID, &run.DBRepoID, seeds)
//
// StoreScenarioSeeds persists seeds, deduping via Supermemory similarity. A new
// seed is skipped only when the top existing scenario matches it at or above
// the dedupe threshold (memory.Thresholds.ScenarioDedupe, default 0.85). This
// mirrors the scenario_trigger gate in the orchestrator: an ungated top-1 hit no
// longer suppresses distinct seeds, so the operator-tunable threshold actually
// applies. A non-positive threshold is clamped to the default so a misconfigured
// 0 can never collapse every distinct seed into "duplicate".
func StoreScenarioSeeds(ctx context.Context, st *store.Store, indexer memory.Indexer, owner, repo string, installationID int64, repoID *int64, dedupeThreshold float64, seeds []ScenarioSeed) {
	for _, seed := range seeds {
		if indexer != nil {
			existing := scenarioSearch(ctx, indexer, slog.Default(), repo, seed.Description, "", 1)
			if isDuplicateScenario(existing, dedupeThreshold) {
				continue // semantically similar scenario already exists
			}
		}
		id, err := st.CreateScenario(ctx, installationID, repoID, seed.Description, seed.Source, seed.SourceRef, seed.Files, nil, seed.Severity)
		if err != nil {
			slog.Warn("failed to create scenario", "error", err, "description", seed.Description)
			continue
		}
		if indexer != nil && id > 0 {
			if err := indexer.IndexScenario(ctx, owner, repo, id, seed.Description, seed.Severity, seed.Files); err != nil {
				slog.Warn("failed to index scenario", "error", err, "id", id)
			} else {
				// Record the deterministic customID (single-sourced via
				// memory.ScenarioCustomID — same repoIDSegment collision-hash the
				// real SM write uses) into 045's mirror column so a NULL
				// supermemory_id means the write failed, not that the pipeline
				// never attempted it. A bare CustomIDSanitize here would drift from
				// the actual doc ID for lossy repo names (e.g. "a.b", "_shared").
				customID := memory.ScenarioCustomID(repo, id)
				if err := st.SetScenarioSupermemoryID(ctx, id, customID); err != nil {
					slog.Warn("write-back scenario SM id", "error", err, "id", id)
				}
			}
		}
	}
}

// scenarioDedupeThreshold clamps the configured dedupe threshold to a positive
// value, falling back to the default. A zero (or negative) threshold makes the
// nearest neighbor count as a duplicate for EVERY seed (Similarity >= 0 is
// always true), silently suppressing all new scenarios — so a misconfigured or
// unset value must never reach the comparison. Unlike other thresholds, an
// explicit 0 here cannot mean "disable": dedupe is a suppression filter, and
// 0 would suppress everything rather than nothing.
func scenarioDedupeThreshold(threshold float64) float64 {
	if threshold <= 0 {
		// Surface the coercion so an operator who configured 0 can discover it.
		slog.Debug("scenario dedupe threshold coerced to default", "configured", threshold, "default", memory.DefaultThresholdScenarioDedupe)
		return memory.DefaultThresholdScenarioDedupe
	}
	return threshold
}

// isDuplicateScenario reports whether the top existing scenario match is close
// enough to treat a candidate seed as a duplicate. Uses the guarded threshold
// so a misconfigured 0 can never collapse distinct scenarios into one.
func isDuplicateScenario(existing []memory.ScenarioSearchResult, dedupeThreshold float64) bool {
	if len(existing) == 0 {
		return false
	}
	return existing[0].Similarity >= scenarioDedupeThreshold(dedupeThreshold)
}

// StorePendingScenarioSeeds stores scenarios as inactive (pending dev approval via reaction).
func StorePendingScenarioSeeds(ctx context.Context, st *store.Store, installationID int64, repoID *int64, seeds []ScenarioSeed) {
	for _, seed := range seeds {
		_, _ = st.CreatePendingScenario(ctx, installationID, repoID, seed.Description, seed.Source, seed.SourceRef, seed.Files, nil, seed.Severity)
	}
}

// FindRelevantScenarios searches for active scenarios matching the changed files.
func FindRelevantScenarios(ctx context.Context, st *store.Store, repoID int64, changedFiles []string) ([]store.Scenario, error) {
	return st.ListScenariosForFiles(ctx, repoID, changedFiles)
}

// FormatScenariosForPrompt renders matched scenarios as an XML block for the review prompt.
func FormatScenariosForPrompt(scenarios []store.Scenario) string {
	if len(scenarios) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n<known_issues>\n")
	sb.WriteString("The following known issues/behaviors are relevant to the files in this PR:\n\n")
	for _, s := range scenarios {
		sb.WriteString(fmt.Sprintf("- [%s] %s (source: %s)\n", s.Severity, s.Description, s.Source))
	}
	sb.WriteString("\nConsider whether the current changes address, worsen, or are unrelated to these known issues.\n")
	sb.WriteString("</known_issues>\n")
	return sb.String()
}
