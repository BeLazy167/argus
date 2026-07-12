package memory

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// indexDismissalCapture drives IndexFeedbackSignal for a dismissed finding
// through the real indexer against a stub Supermemory server, returning the
// customId the write path stamped on the /v3/documents request. It exercises
// the dismissal keying as a write→read round-trip THROUGH the Indexer interface
// — the dismissalCustomID builder is unexported, so its invariants are pinned by
// the value that actually reaches the store, not by poking the builder.
func indexDismissalCapture(t *testing.T, repo, category, body string) string {
	t.Helper()
	var gotCustomID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req AddRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("decode add request: %v", err)
		}
		gotCustomID = req.CustomID
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"doc-1","status":"queued"}`))
	}))
	defer srv.Close()

	idx := NewIndexer(
		NewClient("test-key", WithBaseURL(srv.URL), WithBackoff(BackoffPolicy{MaxAttempts: 1})),
		slog.Default(),
	)
	if err := idx.IndexFeedbackSignal(context.Background(), "acme", repo, FeedbackMemory{
		Action:       "dismissed",
		Category:     category,
		OriginalBody: body,
		Repo:         repo,
	}); err != nil {
		t.Fatalf("IndexFeedbackSignal: %v", err)
	}
	if gotCustomID == "" {
		t.Fatal("write path stamped no customId")
	}
	return gotCustomID
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
