package pipeline

import (
	"strings"
	"testing"
)

// TestCommentTitle covers the headline extraction used in formatCommentBody.
// The function extracts the first sentence when a clean boundary exists, and
// otherwise returns the full `what` (or `body`) truncated at 300 runes.
//
// The sentence-boundary regex was introduced after the naive `strings.Index(". ")`
// split caused production bugs — mid-word periods (v1.2, URLs, "e.g.", "Dr.")
// triggered false-positive splits that rendered as visibly truncated headlines
// like `We read top-l`. Those cases must now fall through to the 300-char cap.
func TestCommentTitle(t *testing.T) {
	tests := []struct {
		name string
		in   FileComment
		want string
	}{
		{
			name: "single sentence — returned as-is",
			in:   FileComment{What: "Null dereference when items is empty."},
			want: "Null dereference when items is empty.",
		},
		{
			name: "multi-sentence — first sentence only",
			in:   FileComment{What: "Null dereference when items is empty. This crashes the billing job."},
			want: "Null dereference when items is empty.",
		},
		{
			name: "mid-word period in version number — no split",
			in:   FileComment{What: "Upgrade to v1.2 broke the parser contract for legacy callers"},
			want: "Upgrade to v1.2 broke the parser contract for legacy callers",
		},
		{
			name: "abbreviation 'e.g.' mid-sentence — no split (lowercase after space)",
			in:   FileComment{What: "Guard for edge cases, e.g. empty arrays and null tokens"},
			want: "Guard for edge cases, e.g. empty arrays and null tokens",
		},
		{
			name: "URL with dots — no split",
			in:   FileComment{What: "The endpoint at example.com/api/v1 returns stale cache"},
			want: "The endpoint at example.com/api/v1 returns stale cache",
		},
		{
			name: "abbreviation 'Dr.' followed by proper noun — splits (matches [A-Z])",
			in:   FileComment{What: "Dr. Smith's patch. The new flow is correct."},
			// Note: this is the one family of false positives the regex can still
			// trip on (period + space + uppercase proper noun). Accepted trade-off
			// versus the much more common "v1.2"/"e.g." patterns the old code broke.
			want: "Dr.",
		},
		{
			name: "empty what — falls back to body",
			in:   FileComment{What: "", Body: "Alternate summary from body."},
			want: "Alternate summary from body.",
		},
		{
			name: "both empty — returns empty string",
			in:   FileComment{What: "", Body: ""},
			want: "",
		},
		{
			name: "question mark + uppercase — splits on '?'",
			in:   FileComment{What: "Why is this nil? The caller always passes a slice."},
			want: "Why is this nil?",
		},
		{
			name: "exclamation + uppercase — splits on '!'",
			in:   FileComment{What: "Data loss! This wipes /data on startup."},
			want: "Data loss!",
		},
		{
			name: "over-long single sentence — truncated at 300 with ellipsis",
			in:   FileComment{What: strings.Repeat("a", 400)},
			// util.Truncate walks back to a rune boundary, so length is <= 303
			// (300 + "..."); we just check prefix length and ellipsis suffix.
			want: strings.Repeat("a", 300) + "...",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := commentTitle(tc.in)
			if got != tc.want {
				t.Errorf("commentTitle\n  in:   %+v\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}
