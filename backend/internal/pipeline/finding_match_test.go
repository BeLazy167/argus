package pipeline

import "testing"

// TestMatcher_Matches proves the proximity / category / line-shift edge cases
// ONCE against the shared predicate, so the three call sites (incremental
// prior-dedup, thread auto-resolve, cross-PR resolved filter) don't each
// re-prove the same rule. Each site's own test now only pins its policy wiring
// and control flow, not the arithmetic.
func TestMatcher_Matches(t *testing.T) {
	cases := []struct {
		name    string
		matcher Matcher
		a, b    Anchor
		want    bool
	}{
		// --- path ---
		{
			name:    "different path never matches",
			matcher: Matcher{Proximity: 10, UseCategory: false},
			a:       Anchor{Path: "a.go", Line: 10},
			b:       Anchor{Path: "b.go", Line: 10},
			want:    false,
		},

		// --- proximity (dedup policy 10) ---
		{
			name:    "exact line within window",
			matcher: Matcher{Proximity: 10, UseCategory: true},
			a:       Anchor{Path: "a.go", Line: 10, Category: "bug"},
			b:       Anchor{Path: "a.go", Line: 10, Category: "bug"},
			want:    true,
		},
		{
			name:    "line shift +10 at boundary — matches",
			matcher: Matcher{Proximity: 10, UseCategory: true},
			a:       Anchor{Path: "a.go", Line: 20, Category: "bug"},
			b:       Anchor{Path: "a.go", Line: 10, Category: "bug"},
			want:    true,
		},
		{
			name:    "line shift -10 at boundary — matches (symmetric)",
			matcher: Matcher{Proximity: 10, UseCategory: true},
			a:       Anchor{Path: "a.go", Line: 10, Category: "bug"},
			b:       Anchor{Path: "a.go", Line: 20, Category: "bug"},
			want:    true,
		},
		{
			name:    "line shift +11 past window — no match",
			matcher: Matcher{Proximity: 10, UseCategory: true},
			a:       Anchor{Path: "a.go", Line: 21, Category: "bug"},
			b:       Anchor{Path: "a.go", Line: 10, Category: "bug"},
			want:    false,
		},

		// --- category ---
		{
			name:    "category mismatch blocks match when UseCategory",
			matcher: Matcher{Proximity: 10, UseCategory: true},
			a:       Anchor{Path: "a.go", Line: 10, Category: "security"},
			b:       Anchor{Path: "a.go", Line: 10, Category: "bug"},
			want:    false,
		},
		{
			name:    "category case-insensitive when UseCategory",
			matcher: Matcher{Proximity: 10, UseCategory: true},
			a:       Anchor{Path: "a.go", Line: 12, Category: "BUG"},
			b:       Anchor{Path: "a.go", Line: 10, Category: "bug"},
			want:    true,
		},
		{
			name:    "category ignored when UseCategory false",
			matcher: Matcher{Proximity: 3, UseCategory: false},
			a:       Anchor{Path: "a.go", Line: 10, Category: "security"},
			b:       Anchor{Path: "a.go", Line: 12, Category: "bug"},
			want:    true,
		},

		// --- auto-resolve policy (3, no category) ---
		{
			name:    "auto-resolve within ±3 — matches",
			matcher: Matcher{Proximity: 3, UseCategory: false},
			a:       Anchor{Path: "auth.go", Line: 10},
			b:       Anchor{Path: "auth.go", Line: 13},
			want:    true,
		},
		{
			name:    "auto-resolve past ±3 — no match",
			matcher: Matcher{Proximity: 3, UseCategory: false},
			a:       Anchor{Path: "auth.go", Line: 10},
			b:       Anchor{Path: "auth.go", Line: 14},
			want:    false,
		},

		// --- cross-PR exact policy (0, no category) ---
		{
			name:    "exact policy requires identical line",
			matcher: Matcher{Proximity: 0, UseCategory: false},
			a:       Anchor{Path: "foo.go", Line: 10},
			b:       Anchor{Path: "foo.go", Line: 10},
			want:    true,
		},
		{
			name:    "exact policy rejects off-by-one",
			matcher: Matcher{Proximity: 0, UseCategory: false},
			a:       Anchor{Path: "foo.go", Line: 10},
			b:       Anchor{Path: "foo.go", Line: 11},
			want:    false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.matcher.Matches(tc.a, tc.b); got != tc.want {
				t.Errorf("Matches(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestParseAnchorKey pins the "<path>:<line>" round-trip the cross-PR filter
// depends on, including the last-colon split (paths may contain colons) and the
// reject cases that make a malformed key a no-op rather than a false match.
func TestParseAnchorKey(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		wantOK   bool
		wantPath string
		wantLine int
	}{
		{"simple", "foo.go:10", true, "foo.go", 10},
		{"nested path", "svc/auth/handler.go:42", true, "svc/auth/handler.go", 42},
		{"path with colon splits on last", "weird:name.go:7", true, "weird:name.go", 7},
		{"empty string", "", false, "", 0},
		{"no colon", "foo.go", false, "", 0},
		{"non-numeric line", "foo.go:abc", false, "", 0},
		{"trailing colon", "foo.go:", false, "", 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			a, ok := parseAnchorKey(tc.key)
			if ok != tc.wantOK {
				t.Fatalf("parseAnchorKey(%q) ok = %v, want %v", tc.key, ok, tc.wantOK)
			}
			if ok && (a.Path != tc.wantPath || a.Line != tc.wantLine) {
				t.Fatalf("parseAnchorKey(%q) = %+v, want {Path:%q Line:%d}", tc.key, a, tc.wantPath, tc.wantLine)
			}
		})
	}
}
