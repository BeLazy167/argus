package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestDeletePatternGuarded(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	installA, _, _ := seedLearnTenant(t, ctx, pool, "mcp-delete-a")
	installB, _, _ := seedLearnTenant(t, ctx, pool, "mcp-delete-b")
	st := NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range []int64{installA, installB} {
			_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, id)
		}
	})
	manual, learned := "manual", "auto_learn"

	// Human-authored, sole owner.
	cid := "sm_del_manual"
	p, err := st.CreatePattern(ctx, installA, nil, "delete me", nil, nil, &manual, nil, nil, &cid, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.DeletePatternGuarded(ctx, p.ID, []int64{installA}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.CustomID != cid || res.Source != "manual" || res.SiblingsAtDelete != 0 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := st.GetPattern(ctx, p.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("row still present: %v", err)
	}
	var deleteEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_id=$2 AND operation='delete'`, installA, p.ID).Scan(&deleteEvents); err != nil || deleteEvents != 1 {
		t.Fatalf("delete outbox events = %d err=%v, want 1", deleteEvents, err)
	}

	// Pipeline-learned: refused without the flag, row untouched; allowed with it.
	lcid := "sm_del_learned"
	l, err := st.CreatePattern(ctx, installA, nil, "learned", nil, nil, &learned, nil, nil, &lcid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeletePatternGuarded(ctx, l.ID, []int64{installA}, false); !errors.Is(err, ErrPipelineLearnedPattern) {
		t.Fatalf("learned without flag: %v", err)
	}
	if _, err := st.GetPattern(ctx, l.ID); err != nil {
		t.Fatalf("refused delete must roll back: %v", err)
	}
	var refusedEvents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_id=$2 AND operation='delete'`, installA, l.ID).Scan(&refusedEvents); err != nil || refusedEvents != 0 {
		t.Fatalf("refused delete must enqueue no tombstone: events=%d err=%v", refusedEvents, err)
	}
	if res, err := st.DeletePatternGuarded(ctx, l.ID, []int64{installA}, true); err != nil || res.Source != "auto_learn" {
		t.Fatalf("learned with flag: res=%+v err=%v", res, err)
	}

	// Legacy siblings: deleting one reports the survivor.
	doc := "sm_del_pair"
	a, err := st.CreatePattern(ctx, installA, nil, "pair", &doc, nil, &manual, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePattern(ctx, installA, nil, "pair", &doc, nil, &manual, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if res, err := st.DeletePatternGuarded(ctx, a.ID, []int64{installA}, false); err != nil || res.SiblingsAtDelete != 1 || res.CustomID != doc {
		t.Fatalf("sibling delete: res=%+v err=%v", res, err)
	}

	// Foreign tenant and missing id are both ErrPatternNotFound.
	fcid := "sm_del_foreign"
	f, err := st.CreatePattern(ctx, installB, nil, "theirs", nil, nil, &manual, nil, nil, &fcid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeletePatternGuarded(ctx, f.ID, []int64{installA}, true); !errors.Is(err, ErrPatternNotFound) {
		t.Fatalf("foreign: %v", err)
	}
	if _, err := st.GetPattern(ctx, f.ID); err != nil {
		t.Fatalf("foreign row must be untouched: %v", err)
	}
	if _, err := st.DeletePatternGuarded(ctx, 1<<40, []int64{installA}, true); !errors.Is(err, ErrPatternNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

// TestDeletePatternGuardedLocksIdentityBeforeTouchingRow is the ABBA-deadlock
// regression for the second delete producer. The guarded path adds a row lock
// (FOR UPDATE, for the source check) that DeletePattern does not have, so it is
// the easiest place to reintroduce row-then-advisory ordering. Held identity key
// => the guarded delete must park on the key with the row still untaken.
func TestDeletePatternGuardedLocksIdentityBeforeTouchingRow(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "mcp-delete-guarded-lock-order")
	st := NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
	})
	src := "manual"
	cid := "sm_delete_guarded_lock_order"
	pattern, err := st.CreatePattern(ctx, install, nil, "doomed", nil, nil, &src, nil, nil, &cid, nil)
	if err != nil {
		t.Fatal(err)
	}
	lockKey := fmt.Sprintf("memory-mirror:%d:%s", install, cid)

	blocker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		t.Fatal(err)
	}
	held := true
	defer func() {
		if held {
			_, _ = blocker.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockKey)
		}
	}()

	deleteDone := make(chan error, 1)
	go func() {
		_, err := st.DeletePatternGuarded(ctx, pattern.ID, []int64{install}, false)
		deleteDone <- err
	}()

	// Wait until the delete is parked on this exact key. A single-argument
	// advisory lock lands in pg_locks with objsubid=1, the key's high word in
	// classid and its low word in objid.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM pg_locks
				WHERE locktype='advisory' AND NOT granted AND objsubid=1
				  AND classid=((hashtextextended($1, 0) >> 32) & 4294967295)::oid
				  AND objid=(hashtextextended($1, 0) & 4294967295)::oid
			)`, lockKey).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-deleteDone:
			t.Fatalf("guarded delete finished without waiting for the identity key: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("guarded delete never waited on the identity key")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The row must still be lockable by an unrelated transaction: neither the
	// FOR UPDATE source read nor the DELETE may have run before the key.
	probe, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	probeTx, err := probe.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var probed int64
	rowErr := probeTx.QueryRow(ctx, `SELECT id FROM patterns WHERE id = $1 FOR UPDATE NOWAIT`, pattern.ID).Scan(&probed)
	_ = probeTx.Rollback(ctx)
	probe.Release()
	if rowErr != nil || probed != pattern.ID {
		t.Fatalf("row was locked or deleted before the identity key was taken: id=%d err=%v", probed, rowErr)
	}

	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockKey); err != nil {
		t.Fatal(err)
	}
	held = false
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("guarded delete after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("guarded delete did not finish after the identity key was released")
	}
}
