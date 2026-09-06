package memory

import (
	"context"
	"testing"
)

// TestSimilarConventionsEndToEnd runs the real query through the real
// scanMatches. The projection and the scanner are two halves of one contract
// that only meet at runtime: when container_tag was added to the shared
// 5-column projection, these two query shapes kept selecting four columns and
// every call started failing with "number of field descriptions must equal
// number of destinations". Nothing caught it, because the only caller
// (orchestrator extractConventions) logs a failed similarity screen and
// carries on with an empty neighbor set — a hard error that reads exactly
// like "no similar conventions found", so the convention is inserted active
// and the conflict checks it exists to feed never run.
//
// So this test asserts on the scanned rows, not on the absence of an error:
// it seeds through the production writer, queries both the categorized and
// the "unknown" (uncategorized) shape, and requires a real match back with
// Metadata["container_tag"] populated — the field only scanMatches can set.
func TestSimilarConventionsEndToEnd(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, &stubEmbedder{}, install, pgTestDims, discardLogger())

	const repo = "argus"
	const category = "error_handling"
	seed := func(content, cat string) string {
		t.Helper()
		res, err := idx.IndexPattern(ctx, repo, PatternMemory{
			Content:  content,
			Source:   "convention_extraction",
			Category: cat,
			PRNumber: 11,
		})
		if err != nil {
			t.Fatalf("seed convention %q: %v", content, err)
		}
		return res.ID
	}
	wantID := seed("Convention [error_handling]: wrap errors with %w at the call site", category)
	// A second convention in another category: the categorized shape must not
	// return it, which is what proves the category clause is still bound to
	// the right parameter after the projection change.
	otherID := seed("Convention [naming]: exported identifiers carry a package-free name", "naming")

	t.Run("categorized", func(t *testing.T) {
		matches, err := idx.SimilarConventions(ctx, repo, category, "Convention [error_handling]: wrap errors at the call site", 5)
		if err != nil {
			t.Fatalf("SimilarConventions: %v", err)
		}
		if len(matches) != 1 {
			t.Fatalf("matches = %d, want 1 (only the error_handling convention)", len(matches))
		}
		m := matches[0]
		if m.ID != wantID {
			t.Fatalf("match ID = %q, want %q", m.ID, wantID)
		}
		if m.Content == "" {
			t.Fatal("match content is empty")
		}
		if got := m.Metadata["container_tag"]; got != RepoTagNew(repo) {
			t.Fatalf("Metadata[container_tag] = %q, want %q", got, RepoTagNew(repo))
		}
		if m.Score < 0 || m.Score > 1 {
			t.Fatalf("score = %v, want within [0,1]", m.Score)
		}
	})

	t.Run("uncategorized", func(t *testing.T) {
		// category "unknown" takes the other query shape, whose parameter
		// numbering differs; both project the same five columns.
		matches, err := idx.SimilarConventions(ctx, repo, "unknown", "Convention: how this repo handles things", 5)
		if err != nil {
			t.Fatalf("SimilarConventions unknown: %v", err)
		}
		if len(matches) != 2 {
			t.Fatalf("matches = %d, want 2 (no category filter)", len(matches))
		}
		seen := map[string]bool{}
		for _, m := range matches {
			seen[m.ID] = true
			if got := m.Metadata["container_tag"]; got != RepoTagNew(repo) {
				t.Fatalf("match %q Metadata[container_tag] = %q, want %q", m.ID, got, RepoTagNew(repo))
			}
		}
		if !seen[wantID] || !seen[otherID] {
			t.Fatalf("matches = %v, want both seeded conventions", seen)
		}
	})
}
