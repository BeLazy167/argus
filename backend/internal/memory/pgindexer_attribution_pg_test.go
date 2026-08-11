package memory

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedAttributionReviews creates a repo and two reviews under the given
// installation so memories.review_id has real FK targets, and returns them.
func seedAttributionReviews(t *testing.T, pool *pgxpool.Pool, install int64) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	var repoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, (random() * 1000000000)::bigint, 'acme/attribution-test')
		RETURNING id`, install).Scan(&repoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	newReview := func(pr int) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `
			INSERT INTO reviews (repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, status)
			VALUES ($1, $2, 'seed', 'someone', 'head', 'base', 'completed')
			RETURNING id`, repoID, pr).Scan(&id); err != nil {
			t.Fatalf("seed review: %v", err)
		}
		return id
	}
	first, second := newReview(1), newReview(2)
	t.Cleanup(func() {
		bg := context.Background()
		// memories.review_id is ON DELETE SET NULL, so rows survive; the pool
		// helper's own cleanup removes them.
		_, _ = pool.Exec(bg, `DELETE FROM reviews WHERE repo_id = $1`, repoID)
		_, _ = pool.Exec(bg, `DELETE FROM repos WHERE id = $1`, repoID)
	})
	return first, second
}

func readReviewID(t *testing.T, pool *pgxpool.Pool, install int64, customID string) *uuid.UUID {
	t.Helper()
	var got *uuid.UUID
	err := pool.QueryRow(context.Background(),
		`SELECT review_id FROM memories WHERE installation_id = $1 AND custom_id = $2`,
		install, customID).Scan(&got)
	if err != nil {
		t.Fatalf("read review_id for %s: %v", customID, err)
	}
	return got
}

// TestPGIndexerStampsReviewAttribution pins the write half of the linkage: an
// indexer bound to a review must stamp every row it writes with that review's
// id, and an unbound one must leave it NULL.
//
// Without the stamp, memory writes are anonymous. A review's memory rows are
// then identifiable only down to the pull request, which every re-review of
// that PR overwrites, so the dashboard and the posted comment have nothing to
// report and a totally dead memory path looks exactly like a healthy one.
func TestPGIndexerStampsReviewAttribution(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	first, second := seedAttributionReviews(t, pool, install)
	logger := slog.New(slog.DiscardHandler)
	base := NewPGIndexer(pool, &stubEmbedder{}, install, pgTestDims, logger)

	doc := func(customID, content string) Doc {
		return Doc{ContainerTag: "acme-api", CustomID: customID, Type: string(TypePattern), Content: content,
			Metadata: map[string]string{"type": string(TypePattern)}}
	}

	tests := []struct {
		name     string
		indexer  Indexer
		customID string
		want     *uuid.UUID
	}{
		{
			name:     "a review-bound indexer stamps its review",
			indexer:  base.ForReview(first),
			customID: "attr--bound",
			want:     &first,
		},
		{
			name:     "an unbound indexer writes no attribution",
			indexer:  base,
			customID: "attr--unbound",
			want:     nil,
		},
		{
			// buildRun's callers can hold a zero review id (a run with no
			// persisted review row). Stamping it would violate the FK and take
			// the real memory content down with it, so ForReview must ignore it
			// rather than pass it through.
			name:     "the nil uuid is not an attribution",
			indexer:  base.ForReview(uuid.Nil),
			customID: "attr--nil",
			want:     nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pg, ok := tc.indexer.(*PGIndexer)
			if !ok {
				t.Fatalf("ForReview returned %T, want *PGIndexer", tc.indexer)
			}
			if err := pg.ImportDocs(ctx, []Doc{doc(tc.customID, "learned something")}); err != nil {
				t.Fatalf("write: %v", err)
			}
			got := readReviewID(t, pool, install, tc.customID)
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("review_id = %s, want NULL", *got)
			case tc.want != nil && got == nil:
				t.Errorf("review_id = NULL, want %s", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Errorf("review_id = %s, want %s", *got, *tc.want)
			}
		})
	}

	t.Run("ForReview does not mutate the indexer it was called on", func(t *testing.T) {
		if base.reviewID != nil {
			t.Fatalf("base indexer picked up attribution %v; the pool and embedder are shared, so a mutating ForReview would make attribution depend on goroutine order", *base.reviewID)
		}
	})

	t.Run("a re-review takes ownership of the row it rewrites", func(t *testing.T) {
		// Writes are upserts keyed on (installation_id, custom_id): the second
		// review genuinely replaced the text, so leaving the first review
		// credited would show it an excerpt it never produced.
		const id = "attr--rewritten"
		firstIdx, _ := base.ForReview(first).(*PGIndexer)
		secondIdx, _ := base.ForReview(second).(*PGIndexer)
		if err := firstIdx.ImportDocs(ctx, []Doc{doc(id, "version one")}); err != nil {
			t.Fatalf("first write: %v", err)
		}
		if err := secondIdx.ImportDocs(ctx, []Doc{doc(id, "version two")}); err != nil {
			t.Fatalf("second write: %v", err)
		}
		got := readReviewID(t, pool, install, id)
		if got == nil || *got != second {
			t.Errorf("review_id = %v, want the rewriting review %s", got, second)
		}
	})
}

// TestPGIndexerAttributionHistoryIsAppendOnly proves deterministic re-upserts
// retain every review that learned a row while memories.review_id continues to
// identify the writer of its current content.
func TestPGIndexerAttributionHistoryIsAppendOnly(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	first, second := seedAttributionReviews(t, pool, install)
	base := NewPGIndexer(pool, nil, install, StorageDimensions, slog.New(slog.DiscardHandler))
	doc := Doc{ContainerTag: "api", CustomID: "attr--history", Type: string(TypePattern), Content: "version one", Metadata: map[string]string{"type": string(TypePattern)}}

	firstIdx := base.ForReview(first).(*PGIndexer)
	if err := firstIdx.ImportDocs(ctx, []Doc{doc}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	doc.Content = "version two"
	secondIdx := base.ForReview(second).(*PGIndexer)
	if err := secondIdx.ImportDocs(ctx, []Doc{doc}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	// Repeating the same review write is idempotent history, not a third event.
	if err := secondIdx.ImportDocs(ctx, []Doc{doc}); err != nil {
		t.Fatalf("repeated second write: %v", err)
	}

	var current uuid.UUID
	var history int
	if err := pool.QueryRow(ctx, `
        SELECT m.review_id, count(a.review_id)
        FROM memories m
        JOIN memory_review_attributions a ON a.memory_id = m.id
        WHERE m.installation_id = $1 AND m.custom_id = $2
        GROUP BY m.review_id`, install, doc.CustomID).Scan(&current, &history); err != nil {
		t.Fatalf("read attribution: %v", err)
	}
	if current != second {
		t.Errorf("current provenance = %s, want %s", current, second)
	}
	if history != 2 {
		t.Errorf("attribution history = %d, want both reviews exactly once", history)
	}
}
