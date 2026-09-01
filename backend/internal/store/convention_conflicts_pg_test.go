package store

import ("context"; "errors"; "testing")

func TestConventionConflictMigrationSmoke(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "convention-conflict-smoke")
	var repo int64
	if err := pool.QueryRow(ctx, `SELECT id FROM repos WHERE installation_id=$1`, install).Scan(&repo); err != nil {
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

func TestConventionEvidenceAndResolutionLifecycle(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "convention-lifecycle")
	var repo int64
	if err := pool.QueryRow(ctx, `SELECT id FROM repos WHERE installation_id=$1`, install).Scan(&repo); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 2)
	custom := []string{"old-convention", "new-convention"}
	for i, c := range custom {
		if err := pool.QueryRow(ctx, `INSERT INTO memories(installation_id,container_tag,custom_id,type,content,metadata) VALUES($1,'convention-lifecycle',$2,'pattern',$2,'{"source":"convention_extraction","category":"testing"}') RETURNING id`, install, c).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	// Same PR evidence retry is idempotent; a distinct PR adds exactly one.
	for _, pr := range []int{7, 7, 8} {
		if _, err := pool.Exec(ctx, `INSERT INTO convention_evidence(convention_memory_id,repo_id,pr_number) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, ids[0], repo, pr); err != nil {
			t.Fatal(err)
		}
	}
	var evidence int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM convention_evidence WHERE convention_memory_id=$1`, ids[0]).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if evidence != 2 {
		t.Fatalf("evidence=%d want 2", evidence)
	}
	if _, err := pool.Exec(ctx, `UPDATE memories SET invalidated_at=now(),superseded_by=$2 WHERE id=$1`, ids[0], ids[1]); err != nil {
		t.Fatal(err)
	}
	var conflictID int64
	if err := pool.QueryRow(ctx, `INSERT INTO convention_conflicts(installation_id,repo_id,category,memory_low_id,memory_high_id,introducing_memory_id,introducing_pr,artifact_comment_id) VALUES($1,$2,'testing',LEAST($3::bigint,$4::bigint),GREATEST($3::bigint,$4::bigint),$4,8,9001) RETURNING id`, install, repo, ids[0], ids[1]).Scan(&conflictID); err != nil {
		t.Fatal(err)
	}
	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM live_memories WHERE id=ANY($1)`, ids).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("open dispute live=%d", live)
	}
	st := NewWithDB(pool)
	if err := st.ResolveConventionConflict(ctx, install, 9001, false); err != nil {
		t.Fatal(err)
	}
	var oldInvalid bool
	var oldSup *int64
	if err := pool.QueryRow(ctx, `SELECT invalidated_at IS NOT NULL,superseded_by FROM memories WHERE id=$1`, ids[0]).Scan(&oldInvalid, &oldSup); err != nil {
		t.Fatal(err)
	}
	if oldInvalid || oldSup != nil {
		t.Fatalf("old was not restored: invalid=%v sup=%v", oldInvalid, oldSup)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM live_memories WHERE id=$1`, ids[0]).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("restored old live=%d", live)
	}
}

func TestConventionConflictDeliveryRetriesAfterFailure(t *testing.T){
 pool,ctx:=fileMemoryTestPool(t);install,_,_:=seedLearnTenant(t,ctx,pool,"conflict-delivery");var repo int64;if err:=pool.QueryRow(ctx,`SELECT id FROM repos WHERE installation_id=$1`,install).Scan(&repo);err!=nil{t.Fatal(err)}
 var a,b int64;for id,c:=range map[*int64]string{&a:"delivery-a",&b:"delivery-b"}{if err:=pool.QueryRow(ctx,`INSERT INTO memories(installation_id,container_tag,custom_id,type,content) VALUES($1,'conflict-delivery',$2,'pattern',$2) RETURNING id`,install,c).Scan(id);err!=nil{t.Fatal(err)}}
 var cid int64;if err:=pool.QueryRow(ctx,`INSERT INTO convention_conflicts(installation_id,repo_id,category,memory_low_id,memory_high_id,introducing_memory_id,introducing_pr) VALUES($1,$2,'testing',LEAST($3::bigint,$4::bigint),GREATEST($3::bigint,$4::bigint),$4,7) RETURNING id`,install,repo,a,b).Scan(&cid);err!=nil{t.Fatal(err)}
 st:=NewWithDB(pool);calls:=0;post:=func(context.Context)(string,int64,error){calls++;if calls==1{return "",0,errors.New("502")};return "node",99,nil}
 if err:=st.DeliverConventionConflict(ctx,cid,post);err==nil{t.Fatal("first delivery succeeded")};if err:=st.DeliverConventionConflict(ctx,cid,post);err!=nil{t.Fatal(err)}
 var delivered bool;if err:=pool.QueryRow(ctx,`SELECT delivered_at IS NOT NULL FROM convention_conflicts WHERE id=$1`,cid).Scan(&delivered);err!=nil||!delivered{t.Fatalf("delivered=%v err=%v",delivered,err)}
}
