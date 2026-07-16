package store

import (
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
		{"heuristic addressed to dismissed (human overrides)", FindingStateAddressed, FindingStateDismissed, true},
		{"heuristic addressed to resolved (manual close)", FindingStateAddressed, FindingStateResolved, true},
		{"addressed cannot regress to posted", FindingStateAddressed, FindingStatePosted, false},
		{"addressed cannot regress to deferred", FindingStateAddressed, FindingStateDeferred, false},
		{"resolved recoverable via human dismissal", FindingStateResolved, FindingStateDismissed, true},
		{"resolved cannot go to addressed", FindingStateResolved, FindingStateAddressed, false},
		{"resolved cannot regress to posted", FindingStateResolved, FindingStatePosted, false},
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
