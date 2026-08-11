package memory

import "testing"

// attributionBand is the measured distribution of matches that are ELIGIBLE to
// attribute — the only population this floor can move.
//
// Measured 2026-08-11 against production installation 285 (963 type=pattern
// memories, 1,052 type=review finding docs). The gate was simulated end to end:
// for each stored finding take its top-1 pattern over its own container plus
// _shared, keep the ones above FindingEnrich, then apply the real
// wordOverlap(match.Content, body) > 0.70 zeroing from pipeline/enricher.go in
// that exact direction. 727 findings clear FindingEnrich and 525 survive the
// overlap guard; those 525 are the denominator below.
//
// Recorded as (cut, share above cut) so the assertions read a MEASURED share
// rather than a hand-picked constant. Deliberately NOT recorded here: any
// "maximum the corpus can reach". An earlier version of this test pinned one
// (0.8649) as a permanent invariant; it was wrong — 97 of these 525 exceed it
// and the highest is 0.9331 — and a false ceiling is worse than no ceiling,
// because it licenses reasoning the corpus does not support.
var attributionBand = []struct {
	cut   float64
	share float64
}{
	{0.70, 1.000},   // 525 of 525
	{0.80, 0.529},   // 278 of 525
	{0.8649, 0.185}, // 97 of 525
	{0.92, 0.008},   // 4 of 525
}

// minAttributionShare is the fraction of eligible matches the floor must still
// admit. A public "we've seen this before" callout that fires on under a
// quarter of the matches that already passed every other gate is not selective,
// it is off: at 0.92 it fired on 0.8% of them.
const minAttributionShare = 0.25

// eligibleShareAtOrAbove returns the measured share of eligible matches a floor
// admits, rounding DOWN to the next recorded cut at or above the floor. Between
// two cuts the true share sits between them, so taking the higher cut's share
// keeps the estimate conservative — this helper can under-credit a floor, never
// over-credit one, which is the safe direction for a lower-bound assertion.
func eligibleShareAtOrAbove(floor float64) float64 {
	for _, b := range attributionBand {
		if b.cut >= floor {
			return b.share
		}
	}
	return 0
}

// TestAttributionFloorSitsInTheMeasuredBand pins the floor from both sides.
//
// Regression guard for #250/#251: the floor was raised to 0.92, which admits
// 0.8% of eligible matches. The raise did not by itself stop attribution — the
// pattern corpus was empty from 2026-06-11 to 2026-08-09, so enrichment
// returned nothing regardless of the floor — but 0.92 would have kept it near
// silent once the corpus came back.
func TestAttributionFloorSitsInTheMeasuredBand(t *testing.T) {
	// Upper bound: the floor must leave attribution firing on a real share of
	// the matches that reach it.
	if got := eligibleShareAtOrAbove(DefaultThresholdAttribution); got < minAttributionShare {
		t.Errorf("attribution floor %.4f admits %.1f%% of eligible matches, want >= %.0f%%; "+
			"re-measure the post-overlap-guard distribution before raising it",
			DefaultThresholdAttribution, got*100, minAttributionShare*100)
	}

	// Lower bound: a public callout must be STRICTLY stricter than the internal
	// enrich gate, which is what the doc comment on the constant claims ("Sits
	// above FindingEnrich"). Equality would announce every match that merely
	// enriched, so `<=` is the failure, not `<`.
	if DefaultThresholdAttribution <= DefaultThresholdFindingEnrich {
		t.Errorf("attribution %.2f does not sit above FindingEnrich %.2f — a match only "+
			"strong enough to enrich internally would be announced publicly",
			DefaultThresholdAttribution, DefaultThresholdFindingEnrich)
	}
}

// TestEligibleShareAtOrAboveIsConservative pins the helper's rounding
// direction. If it rounded to the next cut BELOW the floor instead, a floor of
// 0.85 would report 52.9% (the share at 0.80) when its true share is at most
// 18.5%, and the band assertion above would pass a floor that fails its own
// criterion.
func TestEligibleShareAtOrAboveIsConservative(t *testing.T) {
	tests := []struct {
		name  string
		floor float64
		want  float64
	}{
		{"exact cut returns that cut's share", 0.80, 0.529},
		{"between cuts returns the higher cut", 0.85, 0.185},
		{"below every cut returns the lowest cut", 0.50, 1.000},
		{"above every cut returns zero", 0.99, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eligibleShareAtOrAbove(tt.floor); got != tt.want {
				t.Errorf("eligibleShareAtOrAbove(%.2f) = %.3f, want %.3f", tt.floor, got, tt.want)
			}
		})
	}
}
