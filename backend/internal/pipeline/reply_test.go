package pipeline

import "testing"

// TestReplyLifecycleEvent pins the reply-path authorization gate: the mapping
// from a reply decision to its lifecycle event, AND that thread-resolving /
// terminal-state transitions only fire for a privileged (owner/member/
// collaborator) replier. A non-privileged reply must yield authorized=false so
// the caller skips the transition — an untrusted "I fixed it" can neither resolve
// the thread nor write terminal ledger state (nor override a maintainer's
// dismissal via addressed-by-reply).
func TestReplyLifecycleEvent(t *testing.T) {
	tests := []struct {
		name           string
		action         string
		outcome        string
		assoc          string
		wantEvent      LifecycleEvent
		wantAuthorized bool
	}{
		// Privileged repliers drive the transition.
		{"owner resolve+fixed → addressed-by-reply", "resolve", "confirmed", "OWNER", EventAddressedByReply, true},
		{"member resolve+learning → dismissed", "resolve", "dismissed", "MEMBER", EventDismissed, true},
		{"collaborator not-applicable → dismissed", "not_applicable_change_kind", "not_applicable_change_kind", "COLLABORATOR", EventDismissed, true},

		// Untrusted repliers: correct event mapping but NOT authorized → caller skips.
		{"fork contributor (NONE) resolve+fixed → NOT authorized", "resolve", "confirmed", "NONE", EventAddressedByReply, false},
		{"contributor resolve+learning → NOT authorized", "resolve", "dismissed", "CONTRIBUTOR", EventDismissed, false},
		{"contributor not-applicable → NOT authorized", "not_applicable_change_kind", "not_applicable_change_kind", "CONTRIBUTOR", EventDismissed, false},
		{"empty association → NOT authorized (fail-closed)", "resolve", "confirmed", "", EventAddressedByReply, false},

		// Non-terminal actions raise no transition regardless of privilege.
		{"stand_firm (owner) → no event", "stand_firm", "confirmed", "OWNER", "", false},
		{"clarify (owner) → no event", "clarify", "ignored", "OWNER", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, ok := replyLifecycleEvent(tt.action, tt.outcome, tt.assoc)
			if ev != tt.wantEvent {
				t.Errorf("event = %q, want %q", ev, tt.wantEvent)
			}
			if ok != tt.wantAuthorized {
				t.Errorf("authorized = %v, want %v", ok, tt.wantAuthorized)
			}
		})
	}
}
