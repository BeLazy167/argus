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
			if got := IsArgusThread(tc.login); got != tc.want {
				t.Errorf("IsArgusThread(%q) = %v, want %v", tc.login, got, tc.want)
			}
		})
	}
}
