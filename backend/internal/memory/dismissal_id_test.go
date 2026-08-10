package memory

import (
	"strings"
	"testing"
)

// indexDismissalCapture returns the customID the write path stamps for a
// dismissed finding.
//
// buildFeedbackDoc is the capture point because it is what
// PGIndexer.IndexFeedbackSignal calls to derive the row identity — asserting
// here pins the value that actually reaches the store, one layer above the
// unexported dismissalCustomID builder. (It replaces an HTTP stub that read the
// customId off a request body; the identity is derived in exactly one place
// either way, and that place is this function.)
func indexDismissalCapture(t *testing.T, repo, category, body string) string {
	t.Helper()
	doc, err := buildFeedbackDoc("acme", repo, FeedbackMemory{
		Action:       "dismissed",
		Category:     category,
		OriginalBody: body,
		Repo:         repo,
	})
	if err != nil {
		t.Fatalf("buildFeedbackDoc: %v", err)
	}
	if doc.CustomID == "" {
		t.Fatal("write path stamped no customId")
	}
	return doc.CustomID
}

// TestDismissalCustomID_WriteRoundTrip pins the v2 dismissal keying — category +
// semantic content, deliberately file-path-free, line-number-insensitive,
// repo-scoped — as the customId the write path actually emits.
func TestDismissalCustomID_WriteRoundTrip(t *testing.T) {
	base := indexDismissalCapture(t, "argus", "bug", "unchecked error from os.Open on line 42")

	if len(base) > 100 {
		t.Fatalf("customID exceeds 100 chars: %d", len(base))
	}
	if !strings.Contains(base, "--dismissal") {
		t.Errorf("customID %q missing --dismissal segment", base)
	}
	// The write path stamps exactly the dismissalCustomID key (round-trip anchor).
	if want := dismissalCustomID("argus", "bug", "unchecked error from os.Open on line 42"); base != want {
		t.Errorf("write path customId = %q, want %q", base, want)
	}

	// Stable across restatements that differ only by line number (normalizeBody).
	if got := indexDismissalCapture(t, "argus", "bug", "unchecked error from os.Open on line 99"); got != base {
		t.Errorf("line-number change altered the key: %q vs %q", got, base)
	}
	// Same finding, different category → different doc.
	if got := indexDismissalCapture(t, "argus", "error_handling", "unchecked error from os.Open on line 42"); got == base {
		t.Error("different category must produce a different key")
	}
	// Different content → different doc.
	if got := indexDismissalCapture(t, "argus", "bug", "SQL built via string interpolation"); got == base {
		t.Error("different content must produce a different key")
	}
	// Different repo → different doc.
	if got := indexDismissalCapture(t, "other", "bug", "unchecked error from os.Open on line 42"); got == base {
		t.Error("different repo must produce a different key")
	}
}
