// Package github — sticky_test.go covers the pure-function core of the
// sticky-section editor (replaceOrAppendSection). All cases are table-driven
// subtests so each edge condition is isolated and self-documenting.
package github

import (
	"errors"
	"strings"
	"testing"
)

// TestReplaceOrAppendSection covers append, replace, torn-marker corruption,
// footer-aware idempotence, and name validation.
func TestReplaceOrAppendSection(t *testing.T) {
	const section = "crosspr"
	startMarker := stickyStartMarker(section)
	endMarker := stickyEndMarker(section)
	footer := "_Updated at 12:34 UTC on 2026-04-19_"

	t.Run("appends to empty body", func(t *testing.T) {
		out, changed, err := replaceOrAppendSection("", section, "hello", footer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !changed {
			t.Fatalf("expected changed=true")
		}
		if !strings.Contains(out, startMarker) || !strings.Contains(out, endMarker) {
			t.Fatalf("missing markers in %q", out)
		}
		if !strings.Contains(out, "hello") {
			t.Fatalf("missing section body")
		}
		if !strings.Contains(out, footer) {
			t.Fatalf("missing footer")
		}
	})

	t.Run("appends with blank line separator when body exists", func(t *testing.T) {
		body := "pre-existing content"
		out, changed, err := replaceOrAppendSection(body, section, "new stuff", footer)
		if err != nil || !changed {
			t.Fatalf("unexpected: err=%v changed=%v", err, changed)
		}
		if !strings.HasPrefix(out, "pre-existing content\n\n"+startMarker) {
			t.Fatalf("missing blank line separator: %q", out)
		}
	})

	t.Run("replaces existing section", func(t *testing.T) {
		existing := "head\n\n" + startMarker + "\nold body\n\n_Updated at 10:00 UTC on 2026-04-18_\n" + endMarker + "\ntail"
		out, changed, err := replaceOrAppendSection(existing, section, "new body", footer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !changed {
			t.Fatalf("expected changed=true")
		}
		if strings.Contains(out, "old body") {
			t.Fatalf("old body still present: %q", out)
		}
		if !strings.Contains(out, "new body") {
			t.Fatalf("new body missing: %q", out)
		}
		if !strings.HasPrefix(out, "head\n\n") {
			t.Fatalf("head content damaged: %q", out)
		}
		if !strings.HasSuffix(out, "\ntail") {
			t.Fatalf("tail content damaged: %q", out)
		}
	})

	t.Run("footer-aware idempotence returns changed=false", func(t *testing.T) {
		// Build a body with a different footer but identical content — the
		// idempotence check must strip the footer before diffing.
		body, _, err := replaceOrAppendSection("", section, "stable", "_Updated at 10:00 UTC on 2026-04-18_")
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		out, changed, err := replaceOrAppendSection(body, section, "stable", footer)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if changed {
			t.Fatalf("expected changed=false when content is stable modulo footer")
		}
		if out != body {
			t.Fatalf("expected body to be returned verbatim when unchanged")
		}
	})

	t.Run("start marker only → ErrMarkersCorrupt", func(t *testing.T) {
		body := "prefix\n" + startMarker + "\nstuff without end"
		_, _, err := replaceOrAppendSection(body, section, "x", footer)
		if !errors.Is(err, ErrMarkersCorrupt) {
			t.Fatalf("expected ErrMarkersCorrupt, got %v", err)
		}
	})

	t.Run("end marker only → ErrMarkersCorrupt", func(t *testing.T) {
		body := "prefix\nstuff without start\n" + endMarker
		_, _, err := replaceOrAppendSection(body, section, "x", footer)
		if !errors.Is(err, ErrMarkersCorrupt) {
			t.Fatalf("expected ErrMarkersCorrupt, got %v", err)
		}
	})

	t.Run("end before start → ErrMarkersCorrupt", func(t *testing.T) {
		body := "prefix\n" + endMarker + "\nstuff\n" + startMarker + "\nmore"
		_, _, err := replaceOrAppendSection(body, section, "x", footer)
		if !errors.Is(err, ErrMarkersCorrupt) {
			t.Fatalf("expected ErrMarkersCorrupt, got %v", err)
		}
	})

	t.Run("duplicate start markers → ErrMarkersCorrupt", func(t *testing.T) {
		// User pasted an Argus section snippet into their PR body. The old
		// first-occurrence logic would have replaced the user's paste and
		// orphaned the real section; we now refuse.
		body := startMarker + "\nuser paste\n" + endMarker +
			"\nreal content\n" +
			startMarker + "\nreal section\n" + endMarker
		_, _, err := replaceOrAppendSection(body, section, "x", footer)
		if !errors.Is(err, ErrMarkersCorrupt) {
			t.Fatalf("expected ErrMarkersCorrupt for duplicate start markers, got %v", err)
		}
	})

	t.Run("duplicate end markers → ErrMarkersCorrupt", func(t *testing.T) {
		body := startMarker + "\nstuff\n" + endMarker + "\nmore\n" + endMarker
		_, _, err := replaceOrAppendSection(body, section, "x", footer)
		if !errors.Is(err, ErrMarkersCorrupt) {
			t.Fatalf("expected ErrMarkersCorrupt for duplicate end markers, got %v", err)
		}
	})

	t.Run("empty body → section becomes whole body", func(t *testing.T) {
		out, changed, err := replaceOrAppendSection("", section, "solo", footer)
		if err != nil || !changed {
			t.Fatalf("unexpected: err=%v changed=%v", err, changed)
		}
		expected := startMarker + "\nsolo\n\n" + footer + "\n" + endMarker
		if out != expected {
			t.Fatalf("got %q, want %q", out, expected)
		}
	})

	t.Run("empty sectionMD drops to footer-only inner", func(t *testing.T) {
		out, changed, err := replaceOrAppendSection("", section, "", footer)
		if err != nil || !changed {
			t.Fatalf("unexpected: err=%v changed=%v", err, changed)
		}
		if !strings.Contains(out, footer) {
			t.Fatalf("missing footer: %q", out)
		}
		// Inner should be just the footer with no leading blank line gap.
		inner := extractInner(out, startMarker, endMarker)
		if inner != footer {
			t.Fatalf("expected inner == footer, got %q", inner)
		}
	})
}

// TestIsValidSectionName covers the character-class guard used by the
// UpdateStickySection entry point. Invalid names would corrupt the marker
// comments themselves, so the regex-equivalent check is load-bearing.
func TestIsValidSectionName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty rejected", "", false},
		{"simple lowercase accepted", "crosspr", true},
		{"underscore accepted mid-word", "cross_pr", true},
		{"hyphen accepted mid-word", "cross-pr", true},
		{"digit accepted mid-word", "crosspr2", true},
		{"leading digit rejected", "2crosspr", false},
		{"leading underscore rejected", "_crosspr", false},
		{"leading hyphen rejected", "-crosspr", false},
		{"uppercase rejected", "CrossPR", false},
		{"space rejected", "cross pr", false},
		{"marker-closing sequence rejected", "cross-->", false},
		{"unicode rejected", "crosspré", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := isValidSectionName(tc.in)
			if got != tc.want {
				t.Fatalf("isValidSectionName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
