package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/acmeorg/argus/internal/memory"
	"github.com/acmeorg/argus/internal/rules"
)

// ContextStage retrieves past reviews and rules from Supermemory before the review stage.
type ContextStage struct {
	indexer     *memory.Indexer
	rulesEngine *rules.Engine
	logger      *slog.Logger
}

func NewContextStage(indexer *memory.Indexer, rulesEngine *rules.Engine, logger *slog.Logger) *ContextStage {
	return &ContextStage{indexer: indexer, rulesEngine: rulesEngine, logger: logger}
}

func (cs *ContextStage) Execute(ctx context.Context, run *PipelineRun) error {
	run.Context = make(map[string]ReviewContext)

	// Build triage lookup
	triageLookup := make(map[string]TriageAction)
	for _, t := range run.TriageResults {
		triageLookup[t.File] = t.Action
	}

	// Fetch merged rules (DB + repo file)
	var rulesText []string
	if cs.rulesEngine != nil {
		mergedRules, err := cs.rulesEngine.GetMergedRules(ctx, run.PREvent.InstallationID, run.PREvent.RepoFullName, run.PREvent.HeadRef)
		if err != nil {
			cs.logger.Warn("failed to fetch rules", "error", err)
		} else {
			for _, r := range mergedRules {
				rulesText = append(rulesText, fmt.Sprintf("[%s] %s", r.Category, r.Content))
			}
		}
	}

	// Retrieve context per non-skipped file
	for _, f := range run.Diff.Files {
		if triageLookup[f.NewName] == TriageSkip {
			continue
		}

		rc := ReviewContext{Rules: rulesText}

		// Search past reviews for this file
		if cs.indexer != nil {
			query := fmt.Sprintf("file: %s\n%s", f.NewName, truncate(f.RawDiff, 500))
			results, err := cs.indexer.SearchPastReviews(ctx, run.PREvent.RepoFullName, query, 3)
			if err != nil {
				cs.logger.Warn("past review search failed", "file", f.NewName, "error", err)
			} else {
				for _, r := range results {
					content := r.Memory
					if content == "" {
						content = r.Chunk
					}
					if content != "" {
						rc.PastReviews = append(rc.PastReviews, content)
					}
				}
			}
		}

		run.Context[f.NewName] = rc
	}

	return nil
}

// truncate returns the first maxLen bytes of s.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
