package memory

import (
	"context"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store"
)

// The delete tool's result is a snapshot, not a completion claim: until the
// mirror worker drains, the memory is still searchable even with zero
// siblings; with a sibling, it stays after the drain.
func TestPatternDeleteTimingAgainstMirrorWorker(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	// ClaimMemoryMirrorEvents takes no installation filter, so drain() below
	// claims from the whole outbox — including rows the store and api PG tests
	// write to the same shared database, whose binaries run concurrently under
	// `go test ./...`. A foreign event that failed to apply would fail this
	// test for a reason unrelated to deletion timing. Same guard the other
	// mirror-worker PG tests in this package use.
	lockMirrorOutboxPGTests(t, pool, ctx)
	st := store.NewWithDB(pool)
	if _, err := pool.Exec(ctx, `DELETE FROM memory_mirror_outbox`); err != nil {
		t.Fatalf("clear mirror outbox: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, install)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, install)
	})
	reg := NewRegistry(discardLogger()).WithPostgresBackend(pool,
		NewEmbedderRegistry(noKeysResolver{}, PlatformEmbeddings{Dimensions: StorageDimensions}, discardLogger()))
	worker := NewMirrorWorker(st, func(ctx context.Context, id int64) MirrorIndexer {
		mi, _ := reg.GetIndexer(ctx, id).(MirrorIndexer)
		return mi
	}, discardLogger())
	drain := func() {
		for i := 0; i < 5; i++ {
			n, err := worker.RunOnce(ctx, 100)
			if err != nil {
				t.Fatalf("worker: %v", err)
			}
			if n == 0 {
				return
			}
		}
	}
	live := func(customID string) bool {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM live_memories WHERE installation_id=$1 AND custom_id=$2)`, install, customID).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	src := "manual"

	// Sole owner: delete → still live until drained → gone after.
	cid := SharedPatternCustomID("manual", "sole owner")
	p, err := st.CreatePattern(ctx, install, nil, "sole owner", nil, nil, &src, nil, nil, &cid, nil)
	if err != nil {
		t.Fatal(err)
	}
	drain()
	if !live(cid) {
		t.Fatal("mirror did not index the pattern")
	}
	res, err := st.DeletePatternGuarded(ctx, p.ID, []int64{install}, false)
	if err != nil || res.SiblingsAtDelete != 0 {
		t.Fatalf("delete: res=%+v err=%v", res, err)
	}
	if !live(cid) {
		t.Fatal("memory vanished before the worker ran — the delete result must not be read as completion, and here it would have been true by accident")
	}
	drain()
	if live(cid) {
		t.Fatal("memory still live after the worker drained a sole-owner delete")
	}

	// Sibling remains: delete one → still live after the drain.
	doc := "sm_timing_pair"
	a, err := st.CreatePattern(ctx, install, nil, "pair", &doc, nil, &src, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePattern(ctx, install, nil, "pair", &doc, nil, &src, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	drain()
	res, err = st.DeletePatternGuarded(ctx, a.ID, []int64{install}, false)
	if err != nil || res.SiblingsAtDelete != 1 {
		t.Fatalf("sibling delete: res=%+v err=%v", res, err)
	}
	drain()
	if !live(doc) {
		t.Fatal("memory removed although a sibling pattern still owns it")
	}
}
