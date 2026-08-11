package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// seedLearnTenant creates a throwaway installation + repo + two reviews and
// returns (installationRowID, reviewA, reviewB). Two reviews on ONE repo is the
// shape that matters: memories are upserted per custom_id, so "which review
// wrote this" is only a real question when more than one review could have.
func seedLearnTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, org string) (int64, uuid.UUID, uuid.UUID) {
	t.Helper()

	var installID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random() * 1000000000)::bigint, $1)
		RETURNING id`, org).Scan(&installID); err != nil {
		t.Fatalf("seed installation: %v", err)
	}

	var repoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, (random() * 1000000000)::bigint, $2)
		RETURNING id`, installID, "acme/"+org).Scan(&repoID); err != nil {
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
	a, b := newReview(1), newReview(2)

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memories WHERE installation_id = $1`, installID)
		_, _ = pool.Exec(bg, `DELETE FROM reviews WHERE repo_id = $1`, repoID)
		_, _ = pool.Exec(bg, `DELETE FROM repos WHERE id = $1`, repoID)
		_, _ = pool.Exec(bg, `DELETE FROM installations WHERE id = $1`, installID)
	})
	return installID, a, b
}

// insertLearnedMemory writes one memories row directly. reviewID may be
// uuid.Nil to stand for an unattributed row (a rule, a backfill).
func insertLearnedMemory(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	installID int64, reviewID uuid.UUID, customID, memType, content string, deleted bool,
) {
	t.Helper()
	var rid *uuid.UUID
	if reviewID != uuid.Nil {
		rid = &reviewID
	}
	var del any
	if deleted {
		del = "now()"
	}
	var memoryID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO memories (installation_id, container_tag, custom_id, type, content, review_id, deleted_at)
		VALUES ($1, 'acme-repo', $2, $3, $4, $5, CASE WHEN $6::text IS NULL THEN NULL ELSE now() END)
		RETURNING id`,
		installID, customID, memType, content, rid, del).Scan(&memoryID)
	if err != nil {
		t.Fatalf("insert memory %q: %v", customID, err)
	}
	if rid != nil {
		if _, err := pool.Exec(ctx, `
			INSERT INTO memory_review_attributions (memory_id, review_id)
			VALUES ($1, $2)`, memoryID, *rid); err != nil {
			t.Fatalf("attribute memory %q: %v", customID, err)
		}
	}
}

// TestReviewMemoryReadsAreTenantScoped is the tenant-isolation proof for the
// two review-memory reads. Memory content is derived from private source code,
// so the failure this pins is not a wrong count — it is one installation's code
// knowledge rendered on another installation's review page.
//
// The cross-tenant row is inserted with the SAME review_id as the caller's, the
// only shape that can distinguish "scoped by installation_id" from "scoped by
// review_id and installation_id is decorative". A review id is unguessable but
// not secret: it appears in dashboard URLs and in every posted PR comment.
func TestReviewMemoryReadsAreTenantScoped(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	mine, reviewA, _ := seedLearnTenant(t, ctx, pool, "learn-mine")
	theirs, _, _ := seedLearnTenant(t, ctx, pool, "learn-theirs")

	insertLearnedMemory(t, ctx, pool, mine, reviewA, "mine--pattern", "pattern", "my private pattern", false)
	// Same review id, different tenant. reviews.id is globally unique so this
	// cannot happen naturally — which is exactly why it is the right probe: it
	// isolates the installation predicate from the review predicate.
	insertLearnedMemory(t, ctx, pool, theirs, reviewA, "theirs--pattern", "pattern", "THEIR private pattern", false)

	list, err := st.ListReviewMemories(ctx, mine, reviewA, 0)
	if err != nil {
		t.Fatalf("ListReviewMemories: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListReviewMemories returned %d rows, want 1: %+v", len(list), list)
	}
	if strings.Contains(list[0].Excerpt, "THEIR") {
		t.Errorf("cross-tenant memory content leaked into the response: %q", list[0].Excerpt)
	}

	counts, err := st.CountReviewMemoriesByType(ctx, mine, reviewA)
	if err != nil {
		t.Fatalf("CountReviewMemoriesByType: %v", err)
	}
	if len(counts) != 1 || counts[0].Count != 1 {
		t.Errorf("counts = %+v, want exactly one bucket of 1 (the other tenant's row must not be counted)", counts)
	}

	// The other tenant asking for the same review id gets its own row only.
	theirList, err := st.ListReviewMemories(ctx, theirs, reviewA, 0)
	if err != nil {
		t.Fatalf("ListReviewMemories (theirs): %v", err)
	}
	if len(theirList) != 1 || !strings.Contains(theirList[0].Excerpt, "THEIR") {
		t.Errorf("theirs = %+v, want only their own row", theirList)
	}
}

// TestReviewMemoryTimeIsTheWriteTime pins that the panel dates each entry by
// when THIS review wrote it, not when the row first appeared.
//
// Writes are upserts: a review that re-learns an existing pattern may touch a
// row created weeks earlier. The append-only attribution timestamp records this
// review's write without being overwritten by a later review.
func TestReviewMemoryTimeIsTheWriteTime(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install, reviewA, _ := seedLearnTenant(t, ctx, pool, "learn-time")

	// A row this review rewrote: created long ago, attributed now.
	if _, err := pool.Exec(ctx, `
		WITH inserted AS (
			INSERT INTO memories (installation_id, container_tag, custom_id, type, content, review_id, created_at, updated_at)
			VALUES ($1, 'acme-repo', 'rewritten', 'pattern', 'relearned', $2, now() - interval '30 days', now())
			RETURNING id
		)
		INSERT INTO memory_review_attributions (memory_id, review_id)
		SELECT id, $2 FROM inserted`,
		install, reviewA); err != nil {
		t.Fatalf("seed rewritten memory: %v", err)
	}

	list, err := st.ListReviewMemories(ctx, install, reviewA, 0)
	if err != nil {
		t.Fatalf("ListReviewMemories: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d rows, want 1", len(list))
	}
	if age := time.Since(list[0].WrittenAt); age > time.Hour {
		t.Errorf("WrittenAt is %v old — it is reporting the row's creation date, not this review's write", age)
	}
}

// TestReviewMemoryReadsCarryTheDisplayLabel pins the wire half of the shared
// noun table. The dashboard renders `label` verbatim and derives no noun of its
// own; if these reads stop stamping it, the panel prints "2 " with a blank noun
// while the PR comment about the same review still says "2 PR summaries".
func TestReviewMemoryReadsCarryTheDisplayLabel(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install, reviewA, _ := seedLearnTenant(t, ctx, pool, "learn-label")

	// pr_summary is the type a naive +"s" gets wrong, so it is the one worth
	// asserting: two rows must read "PR summaries", never "PR summarys".
	insertLearnedMemory(t, ctx, pool, install, reviewA, "l--sum-1", "pr_summary", "first summary", false)
	insertLearnedMemory(t, ctx, pool, install, reviewA, "l--sum-2", "pr_summary", "second summary", false)

	counts, err := st.CountReviewMemoriesByType(ctx, install, reviewA)
	if err != nil {
		t.Fatalf("CountReviewMemoriesByType: %v", err)
	}
	if len(counts) != 1 {
		t.Fatalf("counts = %+v, want one bucket", counts)
	}
	if counts[0].Label != "PR summaries" {
		t.Errorf("count label = %q, want %q — the dashboard renders this verbatim",
			counts[0].Label, "PR summaries")
	}

	list, err := st.ListReviewMemories(ctx, install, reviewA, 0)
	if err != nil {
		t.Fatalf("ListReviewMemories: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d rows, want 2", len(list))
	}
	for _, m := range list {
		// One row is one memory, so an entry always reads as the singular.
		if m.Label != "PR summary" {
			t.Errorf("entry label = %q, want %q", m.Label, "PR summary")
		}
	}
}

// TestReviewMemoryReadsSelectTheRightRows covers the remaining row-selection
// contracts: attribution to the asking review, exclusion of unattributed and
// soft-deleted rows, and excerpt truncation.
func TestReviewMemoryReadsSelectTheRightRows(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	install, reviewA, reviewB := seedLearnTenant(t, ctx, pool, "learn-rows")

	long := strings.Repeat("x", LearnedMemoryExcerptChars+500)
	insertLearnedMemory(t, ctx, pool, install, reviewA, "a--sum", "pr_summary", "summary of the PR", false)
	insertLearnedMemory(t, ctx, pool, install, reviewA, "a--pat-1", "pattern", "pattern one", false)
	insertLearnedMemory(t, ctx, pool, install, reviewA, "a--pat-2", "pattern", long, false)
	insertLearnedMemory(t, ctx, pool, install, reviewA, "a--gone", "pattern", "soft deleted", true)
	insertLearnedMemory(t, ctx, pool, install, reviewB, "b--pat", "pattern", "another review's pattern", false)
	insertLearnedMemory(t, ctx, pool, install, uuid.Nil, "unattributed", "rule", "a dashboard rule", false)

	tests := []struct {
		name       string
		reviewID   uuid.UUID
		wantRows   int
		wantCounts map[string]int
	}{
		{
			name:       "only the asking review's live rows",
			reviewID:   reviewA,
			wantRows:   3,
			wantCounts: map[string]int{"pattern": 2, "pr_summary": 1},
		},
		{
			name:       "a sibling review sees only its own",
			reviewID:   reviewB,
			wantRows:   1,
			wantCounts: map[string]int{"pattern": 1},
		},
		{
			name:       "a review that wrote nothing gets an empty answer, not an error",
			reviewID:   uuid.New(),
			wantRows:   0,
			wantCounts: map[string]int{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list, err := st.ListReviewMemories(ctx, install, tc.reviewID, 0)
			if err != nil {
				t.Fatalf("ListReviewMemories: %v", err)
			}
			if len(list) != tc.wantRows {
				t.Errorf("got %d rows, want %d: %+v", len(list), tc.wantRows, list)
			}
			if list == nil {
				t.Error("got nil slice; the handler serializes this straight to JSON and nil renders as null")
			}
			for _, m := range list {
				if len(m.Excerpt) > LearnedMemoryExcerptChars {
					t.Errorf("excerpt is %d chars, want <= %d — an untruncated excerpt puts whole source-derived documents on the wire",
						len(m.Excerpt), LearnedMemoryExcerptChars)
				}
			}

			counts, err := st.CountReviewMemoriesByType(ctx, install, tc.reviewID)
			if err != nil {
				t.Fatalf("CountReviewMemoriesByType: %v", err)
			}
			got := make(map[string]int, len(counts))
			for _, c := range counts {
				got[c.Type] = c.Count
			}
			if len(got) != len(tc.wantCounts) {
				t.Fatalf("counts = %+v, want %+v", got, tc.wantCounts)
			}
			for typ, want := range tc.wantCounts {
				if got[typ] != want {
					t.Errorf("count[%s] = %d, want %d", typ, got[typ], want)
				}
			}
		})
	}
}

func TestReviewMemoryReadsPreserveHistoryAndExcludeNonLiveRows(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	install, reviewA, reviewB := seedLearnTenant(t, ctx, pool, "learn-history")

	insertLearnedMemory(t, ctx, pool, install, reviewA, "shared", "pattern", "current pattern content", false)
	var memoryID int64
	if err := pool.QueryRow(ctx, `
		UPDATE memories SET review_id = $3
		WHERE installation_id = $1 AND custom_id = $2
		RETURNING id`, install, "shared", reviewB).Scan(&memoryID); err != nil {
		t.Fatalf("rewrite current provenance: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memory_review_attributions (memory_id, review_id)
		VALUES ($1, $2)`, memoryID, reviewB); err != nil {
		t.Fatalf("append second attribution: %v", err)
	}

	for _, reviewID := range []uuid.UUID{reviewA, reviewB} {
		list, err := st.ListReviewMemories(ctx, install, reviewID, 0)
		if err != nil {
			t.Fatalf("ListReviewMemories(%s): %v", reviewID, err)
		}
		if len(list) != 1 || list[0].Excerpt != "current pattern content" {
			t.Errorf("review %s memories = %+v, want the retained attribution", reviewID, list)
		}
	}

	if _, err := pool.Exec(ctx, `UPDATE memories SET invalidated_at = now() WHERE id = $1`, memoryID); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	for _, reviewID := range []uuid.UUID{reviewA, reviewB} {
		list, err := st.ListReviewMemories(ctx, install, reviewID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 0 {
			t.Errorf("invalidated row shown to review %s: %+v", reviewID, list)
		}
		counts, err := st.CountReviewMemoriesByType(ctx, install, reviewID)
		if err != nil {
			t.Fatal(err)
		}
		if len(counts) != 0 {
			t.Errorf("invalidated row counted for review %s: %+v", reviewID, counts)
		}
	}
}
