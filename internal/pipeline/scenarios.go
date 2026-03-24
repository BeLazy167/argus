package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/BeLazy167/argus/internal/memory"
	"github.com/BeLazy167/argus/internal/store"
	"github.com/BeLazy167/argus/internal/util"
)

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
// StoreScenarioSeeds persists seeds, deduping via Supermemory similarity.
// If a semantically similar scenario already exists (>0.85), skip the new one.
func StoreScenarioSeeds(ctx context.Context, st *store.Store, indexer *memory.Indexer, owner, repo string, installationID int64, repoID *int64, seeds []ScenarioSeed) {
	for _, seed := range seeds {
		if indexer != nil {
			existing := indexer.SearchScenarios(ctx, owner, repo, seed.Description, "", 1)
			if len(existing) > 0 {
				continue // similar scenario already exists
			}
		}
		id, _ := st.CreateScenario(ctx, installationID, repoID, seed.Description, seed.Source, seed.SourceRef, seed.Files, nil, seed.Severity)
		if indexer != nil && id > 0 {
			indexer.IndexScenario(ctx, owner, repo, id, seed.Description, seed.Severity, seed.Files)
		}
	}
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
