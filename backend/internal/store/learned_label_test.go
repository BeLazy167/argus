package store

import "testing"

// TestLearnedMemoryLabel pins the ONE noun table that both surfaces render.
//
// The PR comment footnote and the dashboard panel used to hold separate tables,
// and the web one pluralized with a naive +"s": the comment said "2 PR
// summaries" while the page said "2 PR summarys" about the same review. There
// is now one table, and the dashboard receives its output as the `label` field
// rather than deriving anything, so these cases cover both surfaces.
func TestLearnedMemoryLabel(t *testing.T) {
	tests := []struct {
		name  string
		typ   string
		count int
		want  string
	}{
		{"regular singular", "pattern", 1, "pattern"},
		{"regular plural", "pattern", 4, "patterns"},
		{"irregular singular", "pr_summary", 1, "PR summary"},
		{"irregular plural: naive +s would print PR summarys", "pr_summary", 3, "PR summaries"},
		{"file memory pluralizes irregularly too", "synthesis", 2, "file memories"},
		{"finding memory", "review", 2, "finding memories"},
		{"architecture note", "topology", 2, "architecture notes"},
		{"feedback signal", "feedback", 2, "feedback signals"},
		{"scenario", "scenario", 2, "scenarios"},
		{"rule", "rule", 2, "rules"},
		// Zero is not a display case — RenderLearnedLine drops empty buckets —
		// but "0 pattern" would be wrong if one ever reached the panel.
		{"zero reads as plural", "pattern", 0, "patterns"},
		// A new backend memory type is still a real write. Reporting the raw
		// type keeps the tally honest instead of dropping the bucket.
		{"an unmapped type degrades to the raw type", "future_type", 2, "future_type"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := LearnedMemoryLabel(tc.typ, tc.count); got != tc.want {
				t.Errorf("LearnedMemoryLabel(%q, %d) = %q, want %q", tc.typ, tc.count, got, tc.want)
			}
		})
	}
}

// TestLearnedNounPluralsAreNotNaive is the guard on the table itself. Every
// entry whose singular ends in "y" or "ry" is one a generic +"s" rule gets
// wrong, so a future edit that "simplifies" the table back into a suffix rule
// fails here rather than on a developer's pull request.
func TestLearnedNounPluralsAreNotNaive(t *testing.T) {
	for typ, nouns := range learnedNoun {
		singular, plural := nouns[0], nouns[1]
		if singular == "" || plural == "" {
			t.Errorf("%s: empty noun pair %q/%q", typ, singular, plural)
			continue
		}
		if singular[len(singular)-1] == 'y' && plural == singular+"s" {
			t.Errorf("%s: plural %q is the naive singular+%q; a word ending in y pluralizes to -ies",
				typ, plural, "s")
		}
	}
}
