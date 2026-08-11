package memory

// Threshold defaults for semantic-search similarity gates. Every learning
// signal that compares similarity against a magic number should read from
// a Thresholds value resolved from settings rather than embedding a literal.
// Bundle 3's rationale: different orgs have different code styles and
// embedding-space distributions, so one-size-fits-all guarantees wrong for
// someone. Lifting to org/repo settings lets operators tune without a deploy.
//
// CALIBRATED against raw cosine similarity. PGIndexer scores are raw cosine
// over voyage-4-large, which sits high for the same semantic distance — a
// floor that looks selective as a fraction is not.
//
// A DISTRIBUTION IS ONLY MEANINGFUL FOR ONE TEXT SHAPE. The earlier
// calibration quoted "top-1 neighbour p50 0.866, p90 1.000" and concluded a
// 0.85 drop floor would over-suppress. That distribution was measured over
// RENDERED GitHub comment bodies — emoji, "**P1 (8/10) · Bug:**", the
// suggestion block, the "React 👎 to dismiss" footer — chrome every finding
// shares, which drags arbitrary pairs together. Re-measured 2026-08-10 on the
// same live corpus, split by shape:
//
//	within-repo top-1, rendered bodies   (n=852)  p50 0.8286  p90 0.9605
//	within-repo top-1, finding statements (n=193) p50 0.4279  p90 0.6408  p99 0.7467
//
// The pipeline compares finding STATEMENTS, so the second row is the only one a
// suppression floor may be read off. Calibrating against the first is what put
// those floors ~0.4 too high and left the gate dead.
//
// The floors below encode each gate's INTENT (how selective it is meant to be)
// against the measured distribution, not a literal number carried over from
// anywhere else.
//
// Re-derive these after any embedding-model change: the model string is the
// space id, and a different space has a different distribution. Re-derive them
// after any change to WHAT TEXT is stored or queried too — that moves the
// distribution just as thoroughly as changing the model.
const (
	// DefaultThresholdFindingEnrich gates the pattern-match lookup that
	// enriches a review comment with "we've seen this before" context. The
	// most recall-oriented gate: a miss costs context, a false hit costs one
	// noisy line. Set near p10 of the measured top-1 distribution.
	DefaultThresholdFindingEnrich = 0.70

	// DefaultThresholdSpecialistMin gates the semantic reads inside
	// SpecialistBlock. Higher than enrichment because specialists care
	// about high-confidence patterns only — irrelevant noise dilutes the
	// prompt budget. Sits between p10 and the median.
	DefaultThresholdSpecialistMin = 0.80

	// DefaultThresholdScenarioTrigger gates whether a simulation-failure
	// match counts as "this scenario triggered." Trigger count feeds into
	// scenario priority; false triggers inflate priority for stale issues.
	// Just above the median, so a merely-related failure does not count.
	DefaultThresholdScenarioTrigger = 0.90

	// DefaultThresholdScenarioDedupe gates scenario-creation dedup. If a
	// candidate scenario matches an existing one above this threshold, we
	// skip creation. Too low = duplicate scenarios; too high = false merges.
	// Near-duplicate detection: genuine duplicates score ~1.0, so this must
	// sit in the top decile or distinct scenarios get silently merged away.
	DefaultThresholdScenarioDedupe = 0.95

	// DefaultThresholdAttribution gates public footer attribution of a pattern
	// match: below it a hit still enriches internally (links + stats) but is
	// NOT surfaced as "we've seen this before" provenance on the comment. Sits
	// above FindingEnrich so only strong matches earn a public callout. This
	// one is publicly visible and wrong attribution is embarrassing, so it is
	// deliberately stricter than the internal enrich gate.
	//
	// 0.80, not 0.92. At 0.92 this floor sat ABOVE what the corpus can produce
	// and attribution stopped entirely: 0 of 228 comments between 2026-07-09 and
	// 2026-08-09, against 44 of 55 before it was raised.
	//
	// Measured on production (see issue #250):
	//   - 964 historical attributed matches, mean score 0.845. 94.2% clear 0.80;
	//     only 4.5% clear 0.92. The raise discarded 95% of matches that fired.
	//   - Across 23,419 lexically-distinct pattern pairs — the population the
	//     wordOverlap guard actually lets through — ZERO reach 0.92. The maximum
	//     is 0.8649.
	//   - Five hand-written paraphrases of real findings scored 0.709-0.883
	//     against their targets (rank 1 of 233 every time) while unrelated
	//     controls scored 0.125-0.259. The band this floor must sit in is the
	//     0.7-0.9 paraphrase band, not the ~1.0 verbatim band.
	//
	// The trap that made 0.92 unreachable rather than merely strict: attribution
	// requires cos > this AND wordOverlap <= 0.70 (enricher.go). Only near-
	// verbatim text reaches 0.92, and near-verbatim text is what the overlap
	// guard deletes. The two conditions are near-mutually-exclusive, so the gate
	// could never fire regardless of corpus size.
	//
	// False-positive cost at 0.80, against those same 23,419 pairs: 11 pairs,
	// 0.047%. Raising this again without re-measuring that distribution will
	// silently switch attribution off a second time.
	DefaultThresholdAttribution = 0.80

	// DefaultThresholdSuppressionDrop gates dismissal-driven DROP: a finding
	// that semantically matches a previously 👎-dismissed finding at/above this
	// score is muted outright (never posted, persisted flagged suppressed).
	//
	// A 👎 means "this finding was a FALSE POSITIVE", so this gate's job is to
	// stop re-posting that same wrong claim HOWEVER it is worded next time. It is
	// not duplicate detection, and the two targets want opposite numbers. The
	// floor was 0.95, justified by a duplicate-detection argument ("true re-posts
	// score ~1.0; the 0.85-0.95 band is similar-but-not-the-same and must NOT be
	// dropped"). Both halves of that argument measure false on the live corpus
	// (2026-08-10), with both sides as finding statements:
	//
	//	same false positive, re-worded, hand-classified   0.8255 .. 0.9570  (n=5)
	//	related-but-distinct dismissal pairs                   max 0.6467  (n=7)
	//	dismissal x every later finding, same repo             max 0.6660  (43x200)
	//	unrelated same-repo neighbours, p99                        0.7467  (n=193)
	//
	// Re-posts only score ~1.0 when both sides are the same TEXT, which is what
	// pipeline.FindingTextFromPostedBody now guarantees — before it, the SAME
	// finding scored 0.7193 mean / 0.9000 max against its own dismissal record.
	// And the 0.83-0.96 band the old comment refused to drop is exactly where the
	// same false positive re-worded actually lands. 0.80 clears all five measured
	// re-wordings, with 0.134 of headroom over the highest coincidence ever
	// observed between a dismissal and a later finding.
	DefaultThresholdSuppressionDrop = 0.80

	// DefaultThresholdSuppressionDowngrade gates dismissal-driven DOWNGRADE and
	// doubles as the "sufficiently similar" bar for a team-feedback streak
	// (SuppressSimilarCount matches in [downgrade, drop) drop the finding too).
	//
	// Under false-positive semantics a single mid-band match is a WEAK verdict,
	// and nothing in the measured corpus lands in [0.75, 0.80) at all — so the
	// band is deliberately narrow and survives for the two jobs that are not
	// weak: exempt findings (security / Law-12 may be lowered but never muted)
	// and the streak rule, which is what catches a repeatedly-rejected class when
	// no single match is strong. Deleting the band would silently kill both.
	//
	// 0.75 sits above EVERY measured false positive — p99 0.7467 of the unrelated
	// same-repo top-1 distribution, and 0.6660, the highest score any dismissal
	// reached against any later finding in the corpus — and below 0.8255, the
	// weakest hand-classified genuine re-wording. It must stay strictly below
	// SuppressionDrop: evaluateDismissals checks the drop rule first, so
	// collapsing the two makes the streak rule unreachable code that still
	// compiles.
	DefaultThresholdSuppressionDowngrade = 0.75
)

// Thresholds carries the resolved per-run similarity gates. The four retrieval
// floors (FindingEnrich/SpecialistMin/ScenarioTrigger/ScenarioDedupe) resolve
// from per-install settings via parseThresholds; the suppression/attribution
// gates are fixed-policy defaults seeded by NewThresholds (not settings-wired —
// the dismissal policy is locked) but live here so EVERY memory similarity gate
// is readable in one struct with no bare literals scattered across call sites.
// Zero-valued fields are normalized to defaults by WithDefaults so callers
// always have valid numbers even if settings are missing or corrupt.
type Thresholds struct {
	FindingEnrich        float64
	SpecialistMin        float64
	ScenarioTrigger      float64
	ScenarioDedupe       float64
	Attribution          float64
	SuppressionDrop      float64
	SuppressionDowngrade float64
}

// NewThresholds returns defaults. Normalization helper lives on the type so
// callers resolving from JSON can do `Thresholds{...}.WithDefaults()`.
func NewThresholds() Thresholds {
	return Thresholds{
		FindingEnrich:        DefaultThresholdFindingEnrich,
		SpecialistMin:        DefaultThresholdSpecialistMin,
		ScenarioTrigger:      DefaultThresholdScenarioTrigger,
		ScenarioDedupe:       DefaultThresholdScenarioDedupe,
		Attribution:          DefaultThresholdAttribution,
		SuppressionDrop:      DefaultThresholdSuppressionDrop,
		SuppressionDowngrade: DefaultThresholdSuppressionDowngrade,
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
// explicit 0. The suppression/attribution fields are always seeded non-zero by
// NewThresholds, so any struct that went through the normal resolve path is
// non-zero; an all-zero struct therefore uniquely identifies the unresolved
// retry/resume case (PipelineRun.Thresholds is json:"-", re-derived by neither),
// which is exactly what WithDefaults normalizes.
func (t Thresholds) IsZero() bool {
	return t.FindingEnrich == 0 && t.SpecialistMin == 0 &&
		t.ScenarioTrigger == 0 && t.ScenarioDedupe == 0 &&
		t.Attribution == 0 && t.SuppressionDrop == 0 && t.SuppressionDowngrade == 0
}

// WithDefaults returns a fully-resolved Thresholds. It substitutes defaults
// ONLY for a completely unconfigured (all-zero) struct — the case where a
// caller passed the zero value rather than a struct resolved from settings.
//
// A struct with ANY non-zero field is treated as already resolved and returned
// verbatim, so an operator's explicit 0 (e.g. "disable this similarity filter",
// per the docs) survives instead of being silently coerced back to the default.
// Per-field zero cannot be distinguished from "unset" once other fields carry
// real values — which is why the normal path resolves every field up front in
// parseThresholds (seeding from NewThresholds, then applying only in-range
// overrides). Not every reader is on that path, though: a retried/resumed run
// hands over the zero value (PipelineRun.Thresholds is json:"-" and is not
// re-derived), so value-level readers a non-resolved struct can still reach —
// briefing assembly, the hint/agentic search floors — call WithDefaults at their
// own boundary to normalize before use. The sole ambiguous input, all seven
// fields set to exactly 0, defaults; that degenerate "disable everything" case
// is not reachable from the normal resolve path.
func (t Thresholds) WithDefaults() Thresholds {
	if t.IsZero() {
		return NewThresholds()
	}
	return t
}
