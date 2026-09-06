package store

import (
	"context"
	"testing"
)

func TestListPatternIDsByIdentity(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	installA, _, _ := seedLearnTenant(t, ctx, pool, "mcp-identity-a")
	installB, _, _ := seedLearnTenant(t, ctx, pool, "mcp-identity-b")
	st := NewWithDB(pool)
	src := "manual"
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range []int64{installA, installB} {
			_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, id)
		}
	})

	// custom-id row, two legacy rows sharing a doc id, and the same custom id
	// in another installation (must never contribute).
	cid := "sm_custom_identity"
	doc := "sm_legacy_doc"
	one, err := st.CreatePattern(ctx, installA, nil, "one", nil, nil, &src, nil, nil, &cid, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy1, err := st.CreatePattern(ctx, installA, nil, "legacy", &doc, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy2, err := st.CreatePattern(ctx, installA, nil, "legacy", &doc, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePattern(ctx, installB, nil, "one", nil, nil, &src, nil, nil, &cid, nil); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListPatternIDsByIdentity(ctx, installA, []string{cid, doc, "absent"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := got[cid]; len(ids) != 1 || ids[0] != one.ID {
		t.Fatalf("custom id → %v, want [%d]", ids, one.ID)
	}
	if ids := got[doc]; len(ids) != 2 || ids[0] != legacy1.ID || ids[1] != legacy2.ID {
		t.Fatalf("legacy doc id → %v, want [%d %d] ordered by id", ids, legacy1.ID, legacy2.ID)
	}
	if _, ok := got["absent"]; ok {
		t.Fatal("absent identity must not appear in the map")
	}
	if empty, err := st.ListPatternIDsByIdentity(ctx, installA, nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty input: %v %v", empty, err)
	}
}
