package store

import "testing"

func TestConventionConflictMigrationSmoke(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "convention-conflict-smoke")
	// seedLearnTenant returns REVIEW ids (uuid), not the repo id; convention_conflicts.repo_id
	// is the bigint repos.id, so read it back from the tenant it just seeded.
	var repo int64
	if err := pool.QueryRow(ctx, `SELECT id FROM repos WHERE installation_id=$1 ORDER BY id DESC LIMIT 1`, install).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 2)
	contents := []string{"Convention [testing]: use testify", "Convention [testing]: use stdlib"}
	for i, content := range contents {
		if err := pool.QueryRow(ctx, `INSERT INTO memories(installation_id,container_tag,custom_id,type,content,metadata) VALUES($1,'api',$2,'pattern',$3,'{"source":"convention_extraction","category":"testing"}') RETURNING id`, install, content, content).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO convention_conflicts(installation_id,repo_id,category,memory_low_id,memory_high_id,introducing_memory_id,introducing_pr) VALUES($1,$2,'testing',LEAST($3::bigint,$4::bigint),GREATEST($3::bigint,$4::bigint),$4,7)`, install, repo, ids[0], ids[1]); err != nil {
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
