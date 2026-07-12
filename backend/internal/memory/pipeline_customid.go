package memory

import (
	"fmt"
	"strings"
)

// ScenarioCustomID returns the stable customId for a scenario doc in the unified
// `{repo}` container. Mirrors the pipeline's inline builder (orchestrator writes
// `{repoIDSegment}--scenario--{id}`) so a backfill / re-index upserts the same
// doc instead of duplicating it. repoIDSegment (not bare CustomIDSanitize) so a
// repo's scenario IDs match its container tag disambiguation for lossy names.
//
// Exported deliberately — a cross-package contract, not a test affordance:
// reconstructed by the pipeline (scenarios.go), cmd/migrate-memory, and
// cmd/reconcile-memory to sync the SAME doc id.
func ScenarioCustomID(repo string, scenarioID int64) string {
	return fmt.Sprintf("%s--scenario--%d", repoIDSegment(repo), scenarioID)
}

// PipelinePatternCustomID reconstructs the deterministic customID the pipeline
// assigned when it first indexed a pattern, so a backfill / re-push upserts the
// SAME Supermemory doc instead of creating a duplicate. The DB `source` column
// diverges from the "segment" the pipeline hashes into the customID
// (orchestrator.go):
//
//	DB source          scope    pipeline write
//	scoring_confirmed  repo     PatternCustomID(repo,"confirmed",content)
//	auto_learn         repo     PatternCustomID(repo,"learned",content)
//	auto_learn         shared   PatternCustomID("","org_learned",content)   (IndexOwnerPattern)
//	convention         repo     PatternCustomID(repo,"convention",rawConvention)  (hashes the RAW
//	                                                                          convention, NOT the
//	                                                                          "Convention [cat]: …" content)
//
// Returns "" for any other source (manual/dashboard/…): those rows are created
// with supermemory_id already set and their SM-side source can differ from the
// DB source, so the caller falls back to the indexer's own default derivation
// (PatternCustomID / SharedPatternCustomID) rather than guessing a wrong ID.
//
// Exported deliberately — reconstructed cross-package by cmd/migrate-memory and
// cmd/reconcile-memory to back-fill legacy patterns onto the same doc id.
func PipelinePatternCustomID(repoName, source, content string, category *string, shared bool) string {
	switch source {
	case "scoring_confirmed":
		return PatternCustomID("", repoName, "confirmed", content)
	case "auto_learn":
		if shared {
			return PatternCustomID("", "", "org_learned", content)
		}
		return PatternCustomID("", repoName, "learned", content)
	case "convention":
		return PatternCustomID("", repoName, "convention", RawConvention(content, category))
	default:
		return ""
	}
}

// RawConvention recovers the un-wrapped convention text the pipeline hashed into
// the convention customID. The pipeline stores content as
// fmt.Sprintf("Convention [%s]: %s", category, convention) but hashes only
// `convention` into PatternCustomID. Stripping the exact wrapper prefix
// reconstructs it; if the wrapper is absent, TrimPrefix returns content
// unchanged so the result stays deterministic.
func RawConvention(content string, category *string) string {
	cat := ""
	if category != nil {
		cat = *category
	}
	return strings.TrimPrefix(content, fmt.Sprintf("Convention [%s]: ", cat))
}
