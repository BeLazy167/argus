package store

import (
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

func TestIncrementPatternMatchUsesDurableCustomIDAfterMirrorRecovery(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	installationID, _, _ := seedLearnTenant(t, ctx, pool, "pattern-stats-custom-id")
	var repoID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM repos WHERE installation_id=$1`, installationID).Scan(&repoID); err != nil {
		t.Fatal(err)
	}
	var patternID int64
	if err := pool.QueryRow(ctx, `INSERT INTO patterns (installation_id,repo_id,content,memory_custom_id) VALUES ($1,$2,'guard writes','repo--learned--abc') RETURNING id`, installationID, repoID).Scan(&patternID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM pattern_stats WHERE installation_id=$1`, installationID)
		_, _ = pool.Exec(ctx, `DELETE FROM patterns WHERE id=$1`, patternID)
	})

	if err := st.IncrementPatternMatch(ctx, patternID); err != nil {
		t.Fatal(err)
	}
	var docID string
	var matches int
	if err := pool.QueryRow(ctx, `SELECT memory_doc_id,times_matched FROM pattern_stats WHERE installation_id=$1`, installationID).Scan(&docID, &matches); err != nil {
		t.Fatal(err)
	}
	if docID != "repo--learned--abc" || matches != 1 {
		t.Fatalf("doc=%q matches=%d", docID, matches)
	}
	if err := st.IncrementPatternMatch(ctx, patternID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT times_matched FROM pattern_stats WHERE installation_id=$1`, installationID).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if matches != 2 {
		t.Fatalf("matches=%d want=2", matches)
	}
}
