package store

import "testing"

func TestConventionConflictMigrationSmoke(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, repo := seedLearnTenant(t, ctx, pool, "convention-conflict-smoke")
	ids := make([]int64, 2)
	contents := []string{"Convention [testing]: use testify", "Convention [testing]: use stdlib"}
	for i, content := range contents {
		if err := pool.QueryRow(ctx, `INSERT INTO memories(installation_id,container_tag,custom_id,type,content,metadata) VALUES($1,'api',$2,'pattern',$3,'{"source":"convention_extraction","category":"testing"}') RETURNING id`, install, content, content).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO convention_conflicts(installation_id,repo_id,category,memory_low_id,memory_high_id,introducing_memory_id,introducing_pr) VALUES($1,$2,'testing',LEAST($3,$4),GREATEST($3,$4),$4,7)`, install, repo, ids[0], ids[1]); err != nil {
		t.Fatal(err)
	}
	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM live_memories WHERE id=ANY($1)`, ids).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("live disputed endpoints=%d want 0", live)
	}
}
