package memory

// Threshold defaults for semantic-search similarity gates. Every learning
// signal that compares similarity against a magic number should read from
// a Thresholds value resolved from settings rather than embedding a literal.
// Bundle 3's rationale: different orgs have different code styles and
// embedding-space distributions, so one-size-fits-all guarantees wrong for
// someone. Lifting to org/repo settings lets operators tune without a deploy.
const (
	// DefaultThresholdFindingEnrich gates the pattern-match lookup that
	// enriches a review comment with "we've seen this before" context. Low
	// (0.5) = broad match; tuning up reduces false-positive enrichments.
	DefaultThresholdFindingEnrich = 0.50

	// DefaultThresholdSpecialistMin gates the semantic reads inside
	// SpecialistBlock. Higher than enrichment because specialists care
	// about high-confidence patterns only — irrelevant noise dilutes the
	// prompt budget.
	DefaultThresholdSpecialistMin = 0.60

	// DefaultThresholdScenarioTrigger gates whether a simulation-failure
	// match counts as "this scenario triggered." Trigger count feeds into
	// scenario priority; false triggers inflate priority for stale issues.
	DefaultThresholdScenarioTrigger = 0.75

	// DefaultThresholdScenarioDedupe gates scenario-creation dedup. If a
	// candidate scenario matches an existing one above this threshold, we
	// skip creation. Too low = duplicate scenarios; too high = false merges.
	DefaultThresholdScenarioDedupe = 0.85
)

// Thresholds carries the resolved per-run similarity gates. Zero-valued fields
// are normalized to defaults by NewThresholds so callers always have valid
// numbers even if settings are missing or corrupt.
type Thresholds struct {
	FindingEnrich   float64
	SpecialistMin   float64
	ScenarioTrigger float64
	ScenarioDedupe  float64
}

// NewThresholds returns defaults. Normalization helper lives on the type so
// callers resolving from JSON can do `Thresholds{...}.WithDefaults()`.
func NewThresholds() Thresholds {
	return Thresholds{
		FindingEnrich:   DefaultThresholdFindingEnrich,
		SpecialistMin:   DefaultThresholdSpecialistMin,
		ScenarioTrigger: DefaultThresholdScenarioTrigger,
		ScenarioDedupe:  DefaultThresholdScenarioDedupe,
	}
}

// SharedConfidenceFloor is the minimum confidence a `_shared` doc must hold
// to be surfaced in specialist retrieval. Docs below this are effectively
// invisible to reviews until the reconciler deletes them (at retirement
// floor SharedRetirementFloor).
const (
	SharedConfidenceFloor    = 0.30
	SharedConfidenceFloorStr = "0.30"
	SharedRetirementFloor    = 0.20
	SharedRetirementFloorStr = "0.20"

	// SharedGraceDays is the window after creation during which `_shared`
	// docs hold full confidence regardless of inactivity — fresh patterns
	// don't decay just because nothing re-referenced them yet.
	SharedGraceDays = 30

	// SharedDecayPerWeek is the confidence drop applied each week past the
	// grace window. 0.05/week + 30-day grace → roughly 6-month lifecycle
	// before retirement: 1.0 → 0.2 in about 16 weeks post-grace.
	SharedDecayPerWeek = 0.05
)

// IsZero reports whether every threshold field is the zero value — i.e. the
// struct was never resolved from settings (a caller passed `Thresholds{}`), as
// opposed to a resolved struct in which an operator set some field to an
// explicit 0.
func (t Thresholds) IsZero() bool {
	return t.FindingEnrich == 0 && t.SpecialistMin == 0 &&
		t.ScenarioTrigger == 0 && t.ScenarioDedupe == 0
}

// WithDefaults returns a fully-resolved Thresholds. It substitutes defaults
// ONLY for a completely unconfigured (all-zero) struct — the case where a
// caller passed the zero value rather than a struct resolved from settings.
//
// A struct with ANY non-zero field is treated as already resolved and returned
// verbatim, so an operator's explicit 0 (e.g. "disable this similarity filter",
// per the docs) survives instead of being silently coerced back to the default.
// Per-field zero cannot be distinguished from "unset" once other fields carry
// real values — which is exactly why parseThresholds resolves every field up
// front (seeding from NewThresholds, then applying only in-range overrides), so
// the value reaching readers here is never partially populated. The sole
// ambiguous input, all four fields set to exactly 0, defaults; that degenerate
// "disable everything" case is not reachable from the normal resolve path.
func (t Thresholds) WithDefaults() Thresholds {
	if t.IsZero() {
		return NewThresholds()
	}
	return t
}
