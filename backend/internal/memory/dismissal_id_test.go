package memory

import (
	"strings"
	"testing"
)

// TestDismissalCustomID pins the v2 dismissal keying: category + semantic
// content, deliberately file-path-free, line-number-insensitive, repo-scoped.
func TestDismissalCustomID(t *testing.T) {
	base := DismissalCustomID("argus", "bug", "unchecked error from os.Open on line 42")

	if base == "" {
		t.Fatal("empty customID")
	}
	if len(base) > 100 {
		t.Fatalf("customID exceeds 100 chars: %d", len(base))
	}
	if !strings.Contains(base, "--dismissal") {
		t.Errorf("customID %q missing --dismissal segment", base)
	}

	// Stable across restatements that differ only by line number (normalizeBody).
	if got := DismissalCustomID("argus", "bug", "unchecked error from os.Open on line 99"); got != base {
		t.Errorf("line-number change altered the key: %q vs %q", got, base)
	}
	// Same finding, different category → different doc.
	if got := DismissalCustomID("argus", "error_handling", "unchecked error from os.Open on line 42"); got == base {
		t.Error("different category must produce a different key")
	}
	// Different content → different doc.
	if got := DismissalCustomID("argus", "bug", "SQL built via string interpolation"); got == base {
		t.Error("different content must produce a different key")
	}
	// Different repo → different doc.
	if got := DismissalCustomID("other", "bug", "unchecked error from os.Open on line 42"); got == base {
		t.Error("different repo must produce a different key")
	}
}
