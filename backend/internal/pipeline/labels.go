package pipeline

import "strings"

// StageOrder lists every pipeline stage key in canonical pipeline-execution
// order. Consumers render rows in this order so two stage tables (PR comment,
// web dashboard) read the same top-to-bottom. Kept in sync with the non-array
// fields of RunTokenUsage — labels_test.go asserts the sync.
var StageOrder = []string{
	"intent",
	"triage",
	"enrichment",
	"conventions",
	"patterns",
	"lead_agent",
	"graph",
	"file_synthesis",
	"review",
	"acceptance",
	"cross_pr",
	"simulation",
	"scoring",
	"synthesis",
	"reply",
}

// SpecialistOrder is the canonical render order for review specialists. Keeps
// the PR-comment table and the web dashboard chart in lockstep. "correctness"
// is the legacy name for what is now "bug_hunter" — kept so old reviews still
// render. "review" is the skim-fallback bucket (no specialist assigned).
var SpecialistOrder = []string{
	"correctness",
	"bug_hunter",
	"security",
	"architecture",
	"regression",
	"review",
}

// stageLabels maps a base stage key to its human-readable title. Lookups that
// miss fall back to the raw key — a new stage wired on the Go side without a
// label entry still shows up, just with the raw identifier.
var stageLabels = map[string]string{
	"intent":         "Intent",
	"triage":         "Triage",
	"enrichment":     "Enrichment",
	"conventions":    "Conventions",
	"patterns":       "Patterns",
	"lead_agent":     "Lead agent",
	"graph":          "Graph",
	"file_synthesis": "File synthesis",
	"review":         "Review",
	"acceptance":     "Acceptance",
	"cross_pr":       "Cross-PR",
	"simulation":     "Simulation",
	"scoring":        "Scoring",
	"synthesis":      "Synthesis",
	"reply":          "Reply",
}

// StageLabel returns the human-readable label for a stage key. Composite keys
// like "review.bug_hunter" or "file_synthesis.src/foo.py" render as
// "Review · bug_hunter" or "File synthesis · src/foo.py". Unknown base keys
// fall through to the raw string so new stages shipped without a label entry
// still appear in the UI — degraded but not missing.
func StageLabel(key string) string {
	base, sub, hasSub := strings.Cut(key, ".")
	baseLabel, ok := stageLabels[base]
	if !ok {
		baseLabel = base
	}
	// Special case: the PR comment uses "review" as both the base stage AND
	// the skim-fallback sub-key (review entry with no Specialist). Rendering
	// "Review · review" is redundant noise — drop the suffix in that case.
	if hasSub && !(base == "review" && sub == "review") {
		return baseLabel + " · " + sub
	}
	return baseLabel
}
