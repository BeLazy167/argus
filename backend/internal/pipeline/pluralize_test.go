package pipeline

import "testing"

// TestPluralize pins the plural-form rule used by buildGitHubReview and the
// synthesis preamble ("1 file", "2 files"). Before the helper existed the code
// emitted "1 files with 1 comments." — the table below locks in the fix so a
// future refactor can't silently revert it.
func TestPluralize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		noun string
		n    int
		want string
	}{
		{"file", 0, "files"},    // zero uses plural (standard English)
		{"file", 1, "file"},     // the specific regression case from PR #335
		{"file", 2, "files"},    // plain plural path
		{"comment", 1, "comment"},
		{"finding", 10, "findings"},
		// Negative counts are not expected in practice but must not panic or
		// produce nonsense — they fall through to the plural branch.
		{"file", -1, "files"},
	}
	for _, tc := range cases {
		if got := pluralize(tc.noun, tc.n); got != tc.want {
			t.Errorf("pluralize(%q, %d) = %q, want %q", tc.noun, tc.n, got, tc.want)
		}
	}
}
