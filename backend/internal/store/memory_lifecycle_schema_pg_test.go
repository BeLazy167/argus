package store

import (
	"testing"
)

func TestMemoryLifecycleSchema(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, reviewA, _ := seedLearnTenant(t, ctx, pool, "lifecycle-schema")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
	})

	var ids []int64
	for _, state := range []string{"live", "deleted", "invalidated", "superseded"} {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO memories (installation_id, container_tag, custom_id, type, content, review_id)
			VALUES ($1, 'api', $2, 'pattern', $2, $3)
			RETURNING id`, install, "schema-"+state, reviewA).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", state, err)
		}
		ids = append(ids, id)
	}
	if _, err := pool.Exec(ctx, `UPDATE memories SET deleted_at = now() WHERE id = $1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memories SET invalidated_at = now() WHERE id = $1`, ids[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memories SET superseded_by = $2 WHERE id = $1`, ids[3], ids[0]); err != nil {
		t.Fatal(err)
	}

	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM live_memories WHERE installation_id = $1`, install).Scan(&live); err != nil {
		t.Fatalf("read live_memories: %v", err)
	}
	if live != 1 {
		t.Fatalf("live_memories returned %d rows, want only the fully live row", live)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO memory_mirror_outbox (installation_id, aggregate_type, aggregate_id, operation, payload)
		VALUES ($1, 'pattern', 7, 'upsert', '{"custom_id":"p-7"}')`, install); err != nil {
		t.Fatalf("insert valid outbox transition: %v", err)
	}
	for _, tc := range []struct{ aggregateType, operation string }{
		{"unknown", "upsert"},
		{"rule", "unknown"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO memory_mirror_outbox (installation_id, aggregate_type, aggregate_id, operation)
			VALUES ($1, $2, 7, $3)`, install, tc.aggregateType, tc.operation); err == nil {
			t.Errorf("outbox accepted aggregate_type=%q operation=%q", tc.aggregateType, tc.operation)
		}
	}
}
