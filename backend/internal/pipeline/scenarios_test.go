package pipeline

import (
	"testing"

	"github.com/BeLazy167/argus/backend/internal/memory"
)

// TestScenarioDedupeThreshold pins the clamp: a non-positive configured
// threshold must fall back to the default, never reach the comparison as 0.
func TestScenarioDedupeThreshold(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{"zero clamps to default", 0, memory.DefaultThresholdScenarioDedupe},
		{"negative clamps to default", -0.5, memory.DefaultThresholdScenarioDedupe},
		{"positive passes through", 0.9, 0.9},
		{"default passes through", memory.DefaultThresholdScenarioDedupe, memory.DefaultThresholdScenarioDedupe},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scenarioDedupeThreshold(tc.in); got != tc.want {
				t.Errorf("scenarioDedupeThreshold(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestIsDuplicateScenario is the regression guard: with a misconfigured 0
// threshold a low-similarity neighbor must NOT be treated as a duplicate (the
// old `Similarity >= 0` path wrongly suppressed every distinct seed).
func TestIsDuplicateScenario(t *testing.T) {
	hit := func(sim float64) []memory.ScenarioSearchResult {
		return []memory.ScenarioSearchResult{{ID: 1, Content: "x", Similarity: sim}}
	}
	tests := []struct {
		name      string
		existing  []memory.ScenarioSearchResult
		threshold float64
		wantDup   bool
	}{
		{"no existing never dup", nil, 0.85, false},
		{"zero threshold, low sim NOT dup (regression)", hit(0.50), 0, false},
		{"zero threshold, high sim dup via default", hit(0.90), 0, true},
		{"zero threshold, just below default not dup", hit(0.84), 0, false},
		{"explicit threshold met", hit(0.86), 0.85, true},
		{"explicit threshold missed", hit(0.84), 0.85, false},
		{"exact boundary is dup", hit(0.85), 0.85, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDuplicateScenario(tc.existing, tc.threshold); got != tc.wantDup {
				t.Errorf("isDuplicateScenario(sim, %v) = %v, want %v", tc.threshold, got, tc.wantDup)
			}
		})
	}
}
