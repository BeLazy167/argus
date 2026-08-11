package store

import (
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/google/uuid"
)

func TestListMemoriesIsTenantScopedLiveFilteredAndStable(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	mine, reviewID, _ := seedLearnTenant(t, ctx, pool, "memory-api-mine")
	theirs, _, _ := seedLearnTenant(t, ctx, pool, "memory-api-theirs")

	insertLearnedMemory(t, ctx, pool, mine, reviewID, "shared-old", "pattern", "retry writes safely", false)
	insertLearnedMemory(t, ctx, pool, mine, reviewID, "shared-new", "pattern", "retry writes atomically", false)
	insertLearnedMemory(t, ctx, pool, mine, reviewID, "wrong-type", "rule", "retry rule", false)
	insertLearnedMemory(t, ctx, pool, mine, reviewID, "deleted", "pattern", "retry deleted", true)
	insertLearnedMemory(t, ctx, pool, mine, reviewID, "invalidated", "pattern", "retry invalidated", false)
	insertLearnedMemory(t, ctx, pool, theirs, uuid.Nil, "foreign", "pattern", "retry foreign secret", false)
	if _, err := pool.Exec(ctx, `UPDATE memories SET container_tag = '_shared' WHERE installation_id = $1`, mine); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memories SET invalidated_at = now() WHERE installation_id = $1 AND custom_id = 'invalidated'`, mine); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Truncate(time.Second)
	if _, err := pool.Exec(ctx, `UPDATE memories SET updated_at = $1 WHERE installation_id = $2 AND custom_id IN ('shared-old','shared-new')`, stamp, mine); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListMemories(ctx, MemoryListFilter{InstallationID: mine, ContainerTags: []string{"_shared"}, Type: "pattern", Query: "retry writes", Limit: 1, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 || len(got.Memories) != 1 {
		t.Fatalf("got total=%d rows=%d: %+v", got.Total, len(got.Memories), got.Memories)
	}
	// Both rows have the same updated_at, so id DESC is the deterministic tie breaker.
	if got.Memories[0].CustomID != "shared-new" {
		t.Fatalf("first=%q want shared-new", got.Memories[0].CustomID)
	}
	if got.Memories[0].ReviewID == nil || *got.Memories[0].ReviewID != reviewID {
		t.Fatalf("review_id=%v want %s", got.Memories[0].ReviewID, reviewID)
	}

	page2, err := st.ListMemories(ctx, MemoryListFilter{InstallationID: mine, ContainerTags: []string{"_shared"}, Type: "pattern", Query: "retry writes", Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page2.Total != 2 || len(page2.Memories) != 1 || page2.Memories[0].CustomID != "shared-old" {
		t.Fatalf("page2=%+v", page2)
	}
}
