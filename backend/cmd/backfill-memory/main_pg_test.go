package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store"
)

func TestRepointPatternsTreatsEveryNonLiveMemoryAsOrphaned(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	st := &store.Store{Pool: pool}

	var install int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random() * 1000000000)::bigint, 'archive-lifecycle-test')
		RETURNING id`).Scan(&install); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM memories WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM installations WHERE id = $1`, install)
	})

	var replacementID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO memories (installation_id, container_tag, custom_id, type, content)
		VALUES ($1, 'api', 'replacement', 'pattern', 'replacement') RETURNING id`, install).Scan(&replacementID); err != nil {
		t.Fatal(err)
	}
	states := []struct {
		name     string
		customID string
	}{
		{"deleted", "archive-deleted"},
		{"invalidated", "archive-invalidated"},
		{"superseded", "archive-superseded"},
	}
	for _, tc := range states {
		var memoryID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO memories (installation_id, container_tag, custom_id, type, content)
			VALUES ($1, 'api', $2, 'pattern', $2) RETURNING id`, install, tc.customID).Scan(&memoryID); err != nil {
			t.Fatal(err)
		}
		switch tc.name {
		case "deleted":
			_, err = pool.Exec(ctx, `UPDATE memories SET deleted_at = now() WHERE id = $1`, memoryID)
		case "invalidated":
			_, err = pool.Exec(ctx, `UPDATE memories SET invalidated_at = now() WHERE id = $1`, memoryID)
		case "superseded":
			_, err = pool.Exec(ctx, `UPDATE memories SET superseded_by = $2 WHERE id = $1`, memoryID, replacementID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO patterns (installation_id, content, memory_doc_id)
			VALUES ($1, $2, $2)`, install, tc.customID); err != nil {
			t.Fatal(err)
		}
	}

	mapped, orphaned, err := repointPatterns(ctx, st, install)
	if err != nil {
		t.Fatalf("repointPatterns: %v", err)
	}
	if mapped != 0 || orphaned != len(states) {
		t.Fatalf("mapped/orphaned = %d/%d, want 0/%d", mapped, orphaned, len(states))
	}
	var remaining int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM patterns WHERE installation_id = $1 AND memory_doc_id IS NOT NULL`, install).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("%d non-live archive pointers remain", remaining)
	}
}
