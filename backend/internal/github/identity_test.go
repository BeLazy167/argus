package github

import "testing"

// TestIsArgusThread is the canonical truth-table for the bot-thread
// predicate. It gates both the `@argus-eye resolve` command and the
// diff-overlap auto-resolve — both rely on this being tight (rejecting
// everything bot-like) and inclusive (catching both login variants).
func TestIsArgusThread(t *testing.T) {
	tests := []struct {
		name  string
		login string
		want  bool
	}{
		{"exact argus-eye login", "argus-eye", true},                  // GraphQL
		{"argus-eye bot suffix", "argus-eye[bot]", true},              // REST
		{"dependabot is not argus", "dependabot[bot]", false},
		{"codecov is not argus", "codecov[bot]", false},
		{"renovate is not argus", "renovate[bot]", false},
		{"cubic is not argus", "cubic-dev-ai[bot]", false},
		{"generic bot suffix no match", "someapp[bot]", false},
		{"human login", "alice", false},
		{"human with digits", "alice42", false},
		{"human with argus in name", "argus-fan", false},
		{"human ending in bot (no brackets)", "robot", false},
		{"empty login", "", false},
		{"case-sensitive — uppercase variant must fail", "Argus-Eye", false},
		{"case-sensitive — [BOT] suffix must fail", "test[BOT]", false},
		{"substring match is not enough", "not-argus-eye", false},
		{"prefix match is not enough", "argus-eye-malicious", false},
		{"bare bot suffix", "[bot]", false},
		{"argus-eye with extra suffix", "argus-eye[bot]-test", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsArgusThread(tc.login, "argus-eye"); got != tc.want {
				t.Errorf("IsArgusThread(%q, %q) = %v, want %v", tc.login, "argus-eye", got, tc.want)
			}
		})
	}
}

// TestIsPrivilegedAssociation is the canonical truth-table for the
// maintainer-trust gate used by `@argus resolve` and the reply-path shortcut:
// only owner/member/collaborator; everyone else (and unknown/empty) is denied
// fail-closed. Case-insensitive + whitespace-trimmed.
func TestIsPrivilegedAssociation(t *testing.T) {
	tests := []struct {
		assoc string
		want  bool
	}{
		{"OWNER", true},
		{"MEMBER", true},
		{"COLLABORATOR", true},
		{"collaborator", true}, // case-insensitive
		{"  member  ", true},   // trimmed
		{"CONTRIBUTOR", false}, // has contributed but is not a maintainer
		{"FIRST_TIME_CONTRIBUTOR", false},
		{"FIRST_TIMER", false},
		{"NONE", false}, // fork contributor on someone else's repo
		{"MANNEQUIN", false},
		{"", false}, // fail-closed on missing association
		{"garbage", false},
	}
	for _, tt := range tests {
		if got := IsPrivilegedAssociation(tt.assoc); got != tt.want {
			t.Errorf("IsPrivilegedAssociation(%q) = %v, want %v", tt.assoc, got, tt.want)
		}
	}
}

// TestIsArgusThreadCustomSlug pins that identity follows the configured App
// slug, not a hardcoded name — self-hosts rename the App freely.
func TestIsArgusThreadCustomSlug(t *testing.T) {
	if !IsArgusThread("my-review-bot", "my-review-bot") {
		t.Error("bare custom slug should match")
	}
	if !IsArgusThread("my-review-bot[bot]", "my-review-bot") {
		t.Error("[bot]-suffixed custom slug should match")
	}
	if IsArgusThread("argus-eye", "my-review-bot") {
		t.Error("default slug must not match when a custom slug is configured")
	}
}
