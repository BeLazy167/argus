package memory

import "testing"

// The suppression floors must sit inside the band that the SAME false positive,
// re-worded, actually occupies.
//
// This is a regression guard for a gate that never fired once. `SELECT count(*)
// FROM review_comments WHERE state='suppressed'` was 0 against 45 dismissals and
// 123 feedback memories, and nothing failed — reviews just kept re-posting
// findings developers had already marked wrong.
//
// The scores below are MEASURED on the live corpus (2026-08-10), not invented,
// and they are measured on the text shape the pipeline actually compares: the
// finding statement on both sides (pipeline.FindingTextFromPostedBody /
// commentTitle). Re-derive them before moving these floors — a floor read off
// the rendered-comment distribution is what put them 0.4 too high.
func TestSuppressionFloorsAdmitMeasuredRewordings(t *testing.T) {
	// Pairs of production dismissals hand-classified as THE SAME false positive
	// stated differently. Both sides carry a 👎, so "this was wrong" is the
	// developer's own verdict on both.
	sameFalsePositive := []struct {
		name  string
		score float64
	}{
		{"OAuth fetch(es) have no timeout", 0.9570},
		{"PostHog shutdown failure swallowed (cross-repo)", 0.9408},
		{"authType schema does not enforce per-mode fields", 0.8600},
		{"revokeAccess error swallowed (two tables)", 0.8337},
		{"non-finite data.cost coerced to zero", 0.8255},
	}

	// Hand-classified the other way: RELATED but a different claim. Suppressing
	// these would mute findings no developer ever rejected. The highest is the
	// SSRF pair — "the ::ffff:172. prefix check is too narrow" against
	// "hex-encoded IPv4-mapped addresses bypass the check", same guard, different
	// defect. 0.6660 is also the ceiling over all 43x200 dismissal-vs-later-
	// finding comparisons in the corpus, so it doubles as the observed
	// false-positive ceiling for the whole gate.
	const highestMeasuredFalsePositive = 0.6660

	for _, m := range sameFalsePositive {
		if m.score < DefaultThresholdSuppressionDrop {
			t.Errorf("drop floor %.2f does not catch %q at %.4f — the same false positive, "+
				"re-worded, is exactly what a 👎 asks this gate to stop re-posting",
				DefaultThresholdSuppressionDrop, m.name, m.score)
		}
	}

	// The other direction. A floor at or below the worst observed coincidence
	// mutes findings nobody dismissed, and a muted finding is only visible in the
	// dashboard, never on the PR.
	if DefaultThresholdSuppressionDowngrade <= highestMeasuredFalsePositive {
		t.Errorf("downgrade floor %.2f is at or below the highest measured false positive "+
			"(%.4f) — every gate action below that line is a measured mistake",
			DefaultThresholdSuppressionDowngrade, highestMeasuredFalsePositive)
	}

	// The band must stay a band. evaluateDismissals checks the drop rule BEFORE
	// the SuppressSimilarCount streak rule, so collapsing downgrade onto drop
	// makes the streak — the mechanism that catches a repeatedly-rejected class
	// when no single match is strong — unreachable code that still compiles.
	if DefaultThresholdSuppressionDowngrade >= DefaultThresholdSuppressionDrop {
		t.Errorf("downgrade %.2f >= drop %.2f — the team-feedback streak rule can never fire",
			DefaultThresholdSuppressionDowngrade, DefaultThresholdSuppressionDrop)
	}

	// A match too weak to be RETRIEVED can never be acted on, so a floor under
	// the retrieval floor is a floor that lies about how selective it is.
	if DefaultThresholdSuppressionDowngrade < DefaultThresholdFindingEnrich {
		t.Errorf("downgrade %.2f is below the FindingEnrich retrieval floor %.2f — "+
			"dismissalSearch would never return a match that low",
			DefaultThresholdSuppressionDowngrade, DefaultThresholdFindingEnrich)
	}
}

// The ceiling. Within-repo top-1 similarity over 193 production findings, both
// sides as finding statements, has p50 0.4279 / p90 0.6408 / p99 0.7467 and a
// single maximum of 0.8696 — which is itself a genuine re-wording, not a
// coincidence. A suppression floor above that maximum cannot fire on anything
// short of byte-identical text, which is the state the gate was already in.
func TestSuppressionDropFloorCanActuallyFire(t *testing.T) {
	const corpusMaxWithinRepo = 0.8696
	if DefaultThresholdSuppressionDrop >= corpusMaxWithinRepo {
		t.Errorf("drop floor %.4f is at or above the highest within-repo similarity the "+
			"corpus reaches (%.4f) — re-measure the distribution before raising this; "+
			"at 0.95 the gate fired 0 times in its entire deployed life",
			DefaultThresholdSuppressionDrop, corpusMaxWithinRepo)
	}
}
