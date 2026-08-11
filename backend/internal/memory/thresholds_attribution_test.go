package memory

import "testing"

// Attribution must sit inside the band real paraphrases actually occupy.
//
// This is a regression guard for a floor that was raised to 0.92 and silently
// switched attribution off for a month — 0 of 228 comments, against 44 of 55
// before. Nothing failed; reviews just stopped saying "we've seen this before".
//
// The scores below are measured, not invented (issue #250): five hand-written
// paraphrases of real production findings, embedded with the deployed provider
// and query shape, scored against their own target memories. Unrelated controls
// on the same corpus scored 0.125-0.259, so the gap this floor has to land in is
// wide and well separated.
func TestAttributionFloorAdmitsMeasuredParaphrases(t *testing.T) {
	measured := []struct {
		name  string
		score float64
	}{
		{"GCS prefix auth bypass", 0.883},
		{"non-idempotent webhook counters", 0.709},
		{"OAuth redirect from Host header", 0.815},
		{"cross-tenant repo lookup", 0.825},
		{"raw token as cache key", 0.734},
	}

	// Not all five: the floor is deliberately stricter than FindingEnrich (0.70)
	// because attribution is publicly visible. But it must admit MOST of the
	// band, and at 0.92 it admitted none of it.
	var admitted int
	for _, m := range measured {
		if m.score > DefaultThresholdAttribution {
			admitted++
		}
	}
	if admitted < 3 {
		t.Errorf("attribution floor %.2f admits only %d of %d measured paraphrases; "+
			"at 0.92 it admitted 0 and attribution stopped firing entirely for a month",
			DefaultThresholdAttribution, admitted, len(measured))
	}

	// The hard ceiling. Across 23,419 lexically-distinct pattern pairs — the
	// population the wordOverlap<=0.70 guard lets reach this gate at all — the
	// single highest similarity observed anywhere in the corpus is 0.8649.
	// A floor above that cannot fire, no matter how much memory accumulates,
	// because the only text that scores higher is the near-verbatim text the
	// overlap guard deletes first.
	const corpusMaxLexicallyDistinct = 0.8649
	if DefaultThresholdAttribution >= corpusMaxLexicallyDistinct {
		t.Errorf("attribution floor %.4f is at or above the highest similarity any "+
			"lexically-distinct pair in the corpus reaches (%.4f) — it can never fire; "+
			"re-measure the distribution before raising this",
			DefaultThresholdAttribution, corpusMaxLexicallyDistinct)
	}

	// Ordering invariant that survives retuning: a public callout must be at
	// least as strict as the internal enrich gate, or attribution would surface
	// matches too weak to have enriched anything.
	if DefaultThresholdAttribution < DefaultThresholdFindingEnrich {
		t.Errorf("attribution %.2f is below FindingEnrich %.2f — a match too weak to "+
			"enrich internally would still be announced publicly",
			DefaultThresholdAttribution, DefaultThresholdFindingEnrich)
	}
}
