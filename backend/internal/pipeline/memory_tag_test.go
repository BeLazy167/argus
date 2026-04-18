package pipeline

import (
	"strings"
	"testing"
)

// TestRenderMemoryTag pins the prose contract for the italic memory-attribution
// line appended to inline review comments. Regressions here change what authors
// see on every repeat-pattern finding — keep the pins tight.
func TestRenderMemoryTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		c      FileComment
		want   string
		prefix string // non-empty ⇒ use HasPrefix; used for timestamp-like outputs
	}{
		{
			name: "no match — empty string",
			c:    FileComment{},
			want: "",
		},
		{
			name: "rule only",
			c:    FileComment{EnforcedRuleContent: "use util.Truncate"},
			want: "_— Matches a repo rule._",
		},
		{
			name: "pattern only, no PR",
			c:    FileComment{MatchedPatternKind: "pattern"},
			want: "_— Matches a prior fix._",
		},
		{
			name: "pattern with PR + author + age",
			c:    FileComment{MatchedPatternKind: "pattern", MatchedPatternPR: 927, MatchedPatternAuthor: "jordan", MatchedPatternAgeDays: 60},
			want: "_— Matches a prior fix in PR #927 (@jordan, 2 months ago)._",
		},
		{
			name: "pattern with PR + author, zero age (drop age clause)",
			c:    FileComment{MatchedPatternKind: "pattern", MatchedPatternPR: 101, MatchedPatternAuthor: "sam"},
			want: "_— Matches a prior fix in PR #101 (@sam)._",
		},
		{
			name: "pattern with PR, no author, with age",
			c:    FileComment{MatchedPatternKind: "pattern", MatchedPatternPR: 42, MatchedPatternAgeDays: 14},
			want: "_— Matches a prior fix in PR #42 (14 days ago)._",
		},
		{
			name: "convention, no category",
			c:    FileComment{MatchedPatternKind: "convention"},
			want: "_— Matches the team's convention._",
		},
		{
			name: "convention with category (underscore normalised)",
			c:    FileComment{MatchedPatternKind: "convention", Category: CategoryErrorHandling},
			want: "_— Matches the team's error handling convention._",
		},
		{
			name: "similarity only",
			c:    FileComment{MatchedPatternKind: "similarity"},
			want: "_— Matches a similar prior finding._",
		},
		{
			name: "rule + pattern collapsed with 'and'",
			c: FileComment{
				EnforcedRuleContent: "x",
				MatchedPatternKind:  "pattern",
				MatchedPatternPR:    7,
			},
			want: "_— Matches a repo rule and a prior fix in PR #7._",
		},
		{
			name: "rule + convention",
			c: FileComment{
				EnforcedRuleContent: "x",
				MatchedPatternKind:  "convention",
				Category:            CategorySecurity,
			},
			want: "_— Matches a repo rule and the team's security convention._",
		},
		{
			name: "unknown kind (no EnforcedRule) → drop silently",
			c:    FileComment{MatchedPatternKind: "wizardry"},
			want: "",
		},
		{
			name: "rule + unknown kind → rule only (unknown dropped)",
			c:    FileComment{EnforcedRuleContent: "x", MatchedPatternKind: "wizardry"},
			want: "_— Matches a repo rule._",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := renderMemoryTag(tc.c)
			if tc.prefix != "" {
				if !strings.HasPrefix(got, tc.prefix) {
					t.Errorf("got %q, want prefix %q", got, tc.prefix)
				}
				return
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestHumanAge pins the calendar-math human phrasing so render tests above
// stay stable when only the age changes.
func TestHumanAge(t *testing.T) {
	t.Parallel()
	cases := map[int]string{
		-1:   "",
		0:    "",
		1:    "1 day ago",
		14:   "14 days ago",
		29:   "29 days ago",
		30:   "1 month ago",
		59:   "1 month ago",
		60:   "2 months ago",
		364:  "12 months ago",
		365:  "1 year ago",
		730:  "2 years ago",
		1500: "4 years ago",
	}
	for days, want := range cases {
		if got := humanAge(days); got != want {
			t.Errorf("humanAge(%d) = %q, want %q", days, got, want)
		}
	}
}

// TestInferMatchKind covers the metadata → kind mapping. Missing or unknown
// source fields fall through to "similarity".
func TestInferMatchKind(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"scoring_confirmed":    "pattern",
		"auto_learn":           "pattern",
		"convention_extraction": "convention",
		"convention":           "convention",
		"pr_summary":           "similarity",
		"arch_summary":         "similarity",
		"synthesis":            "similarity",
		"unknown_source":       "similarity",
		"":                     "similarity",
	}
	for src, want := range cases {
		md := map[string]string{"source": src}
		if src == "" {
			md = nil
		}
		if got := inferMatchKind(md); got != want {
			t.Errorf("inferMatchKind(source=%q) = %q, want %q", src, got, want)
		}
	}
}
