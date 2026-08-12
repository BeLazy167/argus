package memory

import "testing"

// TestAttributionFloorOrdering pins only invariants supported after removal of
// the exact-text overlap guard. The old production percentages used a filtered
// denominator and became stale when exact durable pattern matches were allowed.
func TestAttributionFloorOrdering(t *testing.T) {
	if DefaultThresholdAttribution <= DefaultThresholdFindingEnrich {
		t.Errorf("attribution %.2f must be stricter than internal enrichment %.2f",
			DefaultThresholdAttribution, DefaultThresholdFindingEnrich)
	}
	if DefaultThresholdAttribution > DefaultThresholdSpecialistMin {
		t.Errorf("attribution %.2f exceeds the high-confidence specialist floor %.2f; re-measure before raising it",
			DefaultThresholdAttribution, DefaultThresholdSpecialistMin)
	}
}
