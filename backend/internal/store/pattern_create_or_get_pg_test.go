package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func countPatternsAndEvents(t *testing.T, ctx context.Context, st *Store, installationID int64, identity string) (int, int) {
	t.Helper()
	var patterns, events int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE installation_id=$1 AND `+patternIdentityExpr+` = $2`, installationID, identity).Scan(&patterns); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_type='pattern'`, installationID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	return patterns, events
}

func TestCreateOrGetPattern(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "mcp-create-or-get")
	st := NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
	})
	src, author := "manual", "user_a"
	cid := "sm_create_or_get"

	first, created, err := st.CreateOrGetPattern(ctx, install, nil, "content", &author, &src, nil, cid, map[string]string{"created_by": author, "origin": "mcp"})
	if err != nil || !created {
		t.Fatalf("first: created=%v err=%v", created, err)
	}
	if first.MemoryCustomID == nil || *first.MemoryCustomID != cid {
		t.Fatalf("GetPattern must expose memory_custom_id: %+v", first)
	}
	if p, e := countPatternsAndEvents(t, ctx, st, install, cid); p != 1 || e != 1 {
		t.Fatalf("after first: patterns=%d events=%d, want 1/1", p, e)
	}

	// Exact retry by a different author: same row, author untouched, no
	// second row and — the point — no second outbox event.
	other := "user_b"
	again, created, err := st.CreateOrGetPattern(ctx, install, nil, "content", &other, &src, nil, cid, nil)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("retry: created=%v id=%d want %d err=%v", created, again.ID, first.ID, err)
	}
	if again.CreatedBy == nil || *again.CreatedBy != author {
		t.Fatalf("retry changed created_by to %v", again.CreatedBy)
	}
	if p, e := countPatternsAndEvents(t, ctx, st, install, cid); p != 1 || e != 1 {
		t.Fatalf("after retry: patterns=%d events=%d, want unchanged 1/1", p, e)
	}

	// Legacy siblings: lowest id wins deterministically.
	doc := "sm_legacy_pair"
	low, err := st.CreatePattern(ctx, install, nil, "legacy", &doc, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePattern(ctx, install, nil, "legacy", &doc, nil, &src, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, created, err := st.CreateOrGetPattern(ctx, install, nil, "legacy", &author, &src, nil, doc, nil)
	if err != nil || created || got.ID != low.ID {
		t.Fatalf("legacy siblings: got=%d created=%v want lowest %d err=%v", got.ID, created, low.ID, err)
	}

	if _, err := st.FindPatternIDByIdentity(ctx, install, "absent"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("absent identity: %v", err)
	}
}

// Two concurrent callers racing on an absent identity produce one row and one
// outbox event. This is the property a handler-level check-then-insert lacks.
func TestCreateOrGetPatternConcurrent(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "mcp-create-race")
	st := NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
	})
	src := "manual"
	const cid = "sm_race"
	const callers = 8
	// Warm the pool, then release the barrier. A cold pgxpool opens each
	// connection on first use, and those handshakes stagger the callers far
	// more than the transaction they are racing on: without both steps the
	// eight goroutines mostly run one after another and the test cannot see a
	// missing lock.
	warm := make([]*pgxpool.Conn, 0, callers)
	for i := 0; i < callers && int32(i) < pool.Config().MaxConns; i++ {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		warm = append(warm, conn)
	}
	for _, conn := range warm {
		conn.Release()
	}

	var wg, ready sync.WaitGroup
	start := make(chan struct{})
	createdCount := make(chan bool, callers)
	ready.Add(callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			_, created, err := st.CreateOrGetPattern(ctx, install, nil, "raced", nil, &src, nil, cid, nil)
			if err != nil {
				t.Errorf("caller: %v", err)
				return
			}
			createdCount <- created
		}()
	}
	ready.Wait()
	close(start)
	wg.Wait()
	close(createdCount)
	n := 0
	for c := range createdCount {
		if c {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d callers reported created=true, want exactly 1", n)
	}
	if p, e := countPatternsAndEvents(t, ctx, st, install, cid); p != 1 || e != 1 {
		t.Fatalf("patterns=%d events=%d, want 1/1", p, e)
	}
}

// DeletePattern must take the per-identity producer key BEFORE it deletes the
// row. CreatePattern locks first and then upserts; a delete that wrote the row
// first and locked afterwards would invert the order and deadlock against a
// concurrent re-learn of the same content-derived identity.
//
// The proof is tuple-level and deterministic, not timing-based: while the
// delete is parked on the advisory key, a second session takes the target row
// FOR UPDATE NOWAIT. That succeeds only if the blocked transaction has not
// touched the row yet — an already-executed DELETE would hold the tuple and
// raise 55P03. A plain visibility check would prove nothing, because an
// uncommitted DELETE is invisible to other sessions either way.
func TestDeletePatternLocksIdentityBeforeDeletingRow(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "mcp-delete-lock-order")
	st := NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
	})
	src := "manual"
	cid := "sm_delete_lock_order"
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
	go func() { deleteDone <- st.DeletePattern(ctx, pattern.ID, []int64{install}) }()

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
			t.Fatalf("delete finished without waiting for the identity key: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("delete never waited on the identity key")
		}
		time.Sleep(10 * time.Millisecond)
	}

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
		t.Fatalf("row was already written before the identity key was taken: id=%d err=%v", probed, rowErr)
	}

	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockKey); err != nil {
		t.Fatal(err)
	}
	held = false
	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("delete after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not finish after the identity key was released")
	}

	var rows, tombstones int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE id = $1`, pattern.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM memory_mirror_outbox WHERE installation_id = $1 AND aggregate_id = $2 AND operation = $3`,
		install, pattern.ID, MemoryMirrorDelete).Scan(&tombstones); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || tombstones != 1 {
		t.Fatalf("after release: patterns=%d delete events=%d, want 0/1", rows, tombstones)
	}
}

// TestCreatePatternLocksIdentityBeforeWritingRow pins the ORDER inside
// CreatePattern's transaction: the per-identity advisory lock is taken before
// the row is inserted. CreateOrGetPattern's "does this identity already exist"
// recheck is only meaningful if every writer holds that key first; drop the
// lock call and two concurrent writers each see no row and each insert one.
//
// Nothing else in the suite fails when the lock call is deleted, which is why
// this test exists. It is the CreatePattern half of
// TestDeletePatternLocksIdentityBeforeDeletingRow.
func TestCreatePatternLocksIdentityBeforeWritingRow(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	install, _, _ := seedLearnTenant(t, ctx, pool, "mcp-create-lock-order")
	st := NewWithDB(pool)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
	})
	src := "manual"
	cid := "sm_create_lock_order"
	lockKey := fmt.Sprintf("memory-mirror:%d:%s", install, cid)

	// Hold the identity key from a connection of our own, before the identity
	// names any row at all.
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

	createDone := make(chan error, 1)
	go func() {
		_, err := st.CreatePattern(ctx, install, nil, "guarded", nil, nil, &src, nil, nil, &cid, nil)
		createDone <- err
	}()

	// Wait until the create is parked on this exact key, and capture the
	// backend that is parked. A single-argument advisory lock lands in pg_locks
	// with objsubid=1, the key's high word in classid and its low word in objid.
	var blockedPID int32
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := pool.QueryRow(ctx, `
			SELECT pid FROM pg_locks
			WHERE locktype='advisory' AND NOT granted AND objsubid=1
			  AND classid=((hashtextextended($1, 0) >> 32) & 4294967295)::oid
			  AND objid=(hashtextextended($1, 0) & 4294967295)::oid
			LIMIT 1`, lockKey).Scan(&blockedPID)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case err := <-createDone:
			t.Fatalf("create finished without waiting for the identity key: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("create never waited on the identity key")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The row must not be written yet — and "not yet" has to be read from the
	// blocked backend's own locks, not from committed rows. Its INSERT is
	// invisible to every other transaction until commit, so counting rows here
	// reports zero either way and cannot fail. What a write does leave behind
	// immediately is a RowExclusiveLock on patterns held by that backend.
	//
	// This is the assertion the ordering rests on: enqueueMemoryMirrorEvent
	// takes the SAME identity key later in the transaction, so deleting
	// CreatePattern's lock call still parks the transaction on this key — just
	// with the row already inserted behind it, which is precisely the ordering
	// the recheck cannot survive.
	var wrotePatterns bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_locks
			WHERE pid = $1 AND locktype = 'relation'
			  AND relation = 'patterns'::regclass
			  AND mode = 'RowExclusiveLock'
		)`, blockedPID).Scan(&wrotePatterns); err != nil {
		t.Fatal(err)
	}
	if wrotePatterns {
		t.Fatal("the row was written before the identity key was taken; the lock must come first")
	}
	if patterns, events := countPatternsAndEvents(t, ctx, st, install, cid); patterns != 0 || events != 0 {
		t.Fatalf("while parked on the identity key: patterns=%d events=%d, want 0/0", patterns, events)
	}

	if _, err := blocker.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockKey); err != nil {
		t.Fatal(err)
	}
	held = false
	select {
	case err := <-createDone:
		if err != nil {
			t.Fatalf("create after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("create did not finish after the identity key was released")
	}

	if patterns, events := countPatternsAndEvents(t, ctx, st, install, cid); patterns != 1 || events != 1 {
		t.Fatalf("after release: patterns=%d events=%d, want 1/1", patterns, events)
	}
}
