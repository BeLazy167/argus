package store

import (
	"sort"
	"testing"
)

func TestCanTransitionFindingState(t *testing.T) {
	tests := []struct {
		name string
		from FindingState
		to   FindingState
		want bool
	}{
		{"posted to addressed", FindingStatePosted, FindingStateAddressed, true},
		{"posted to dismissed", FindingStatePosted, FindingStateDismissed, true},
		{"posted to deferred", FindingStatePosted, FindingStateDeferred, true},
		{"deferred to addressed", FindingStateDeferred, FindingStateAddressed, true},
		{"deferred to dismissed", FindingStateDeferred, FindingStateDismissed, true},
		{"dismissed to addressed (dev fixed it after arguing)", FindingStateDismissed, FindingStateAddressed, true},
		{"idempotent replay: dismissed to dismissed", FindingStateDismissed, FindingStateDismissed, true},
		{"addressed is terminal", FindingStateAddressed, FindingStateDismissed, false},
		{"addressed cannot regress to posted", FindingStateAddressed, FindingStatePosted, false},
		{"suppressed is terminal", FindingStateSuppressed, FindingStateDismissed, false},
		{"nothing transitions INTO suppressed", FindingStatePosted, FindingStateSuppressed, false},
		{"nothing transitions back to posted", FindingStateDeferred, FindingStatePosted, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanTransitionFindingState(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransitionFindingState(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

// TestFindingStateSources pins the WHERE-clause source sets UpdateFindingState
// derives — the SQL guard IS the state machine at the persistence seam.
func TestFindingStateSources(t *testing.T) {
	tests := []struct {
		to   FindingState
		want []string
	}{
		{FindingStateAddressed, []string{"addressed", "deferred", "dismissed", "posted"}},
		{FindingStateDismissed, []string{"deferred", "dismissed", "posted"}},
		{FindingStateDeferred, []string{"deferred", "posted"}},
		// suppressed is insert-only: its only "source" is itself (idempotence).
		{FindingStateSuppressed, []string{"suppressed"}},
	}
	for _, tt := range tests {
		t.Run(string(tt.to), func(t *testing.T) {
			got := findingStateSources(tt.to)
			sort.Strings(got)
			if len(got) != len(tt.want) {
				t.Fatalf("sources(%s) = %v, want %v", tt.to, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("sources(%s) = %v, want %v", tt.to, got, tt.want)
				}
			}
		})
	}
}
