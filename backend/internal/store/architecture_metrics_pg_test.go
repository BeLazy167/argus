package store

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

func TestListArchCouplingUsesActualChangedFiles(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	tenant := seedBlastTenant(t, ctx, pool, "arch-coupling")
	reviewID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO reviews (id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, status)
		VALUES ($1, $2, 17, 'clean change', 'dev', 'head', 'base', 'completed')`, reviewID, tenant.repoA); err != nil {
		t.Fatalf("seed review: %v", err)
	}

	oldPayload, _ := json.Marshal(map[string]any{"Diff": map[string]any{"Files": []map[string]any{{"NewName": "stale.go"}}}})
	newPayload, _ := json.Marshal(map[string]any{"Diff": map[string]any{"Files": []map[string]any{
		{"NewName": "clean.go", "Status": "modified"},
		{"NewName": "/dev/null", "OldName": "deleted.go", "Status": "deleted"},
	}}})
	if _, err := pool.Exec(ctx, `
		INSERT INTO pipeline_states (id, review_id, state, payload, updated_at)
		VALUES ($1, $2, 'reviewing', $3, NOW() - INTERVAL '1 minute'),
		       ($4, $2, 'completed', $5, NOW())`, uuid.New(), reviewID, oldPayload, uuid.New(), newPayload); err != nil {
		t.Fatalf("seed pipeline states: %v", err)
	}

	rows, err := st.ListArchCoupling(ctx, tenant.repoA)
	if err != nil {
		t.Fatalf("ListArchCoupling: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one PR", rows)
	}
	got := map[string]bool{}
	for _, file := range rows[0].Files {
		got[file] = true
	}
	if !got["clean.go"] || !got["deleted.go"] || got["stale.go"] {
		t.Fatalf("changed files = %v, want clean.go + deleted.go from latest state", rows[0].Files)
	}
}
