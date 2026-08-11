package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func lockMirrorOutboxPGTests(t *testing.T, pool *pgxpool.Pool, ctx context.Context) {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire mirror test lock connection: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(90254)`); err != nil {
		conn.Release()
		t.Fatalf("acquire mirror test lock: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(90254)`)
		conn.Release()
	})
}

// A pipeline writes memory first with a review-bound indexer, then persists the
// patterns row whose outbox event repairs a failed first write. Replaying that
// repair over an already-live row must not replace the pipeline's richer shape
// with the reduced relational projection or clear current review provenance.
func TestMirrorWorkerPreservesPipelinePatternProvenanceAndMetadata(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	reviewID, _ := seedAttributionReviews(t, pool, install)
	embedder := &stubEmbedder{}
	base := NewPGIndexer(pool, embedder, install, pgTestDims, slog.New(slog.DiscardHandler))

	const (
		originRepo = "acme/attribution-test"
		content    = "generic guard writes before publishing"
		category   = "correctness"
	)
	customID := PatternCustomID("", "", "org_learned", content)
	pipelinePattern := PatternMemory{
		Content:  content,
		CustomID: customID,
		Source:   "auto_learn",
		Category: category,
		PRNumber: 41,
		Score:    97,
		Extra: map[string]string{
			"repo":         originRepo,
			"origin_stage": "scoring",
		},
	}
	if _, err := base.ForReview(reviewID).IndexSharedPattern(ctx, pipelinePattern); err != nil {
		t.Fatalf("pipeline shared pattern write: %v", err)
	}
	before := readRow(t, pool, install, customID)
	beforeReview := readReviewID(t, pool, install, customID)
	beforeEmbedCalls := embedder.calls

	// This is the intentionally smaller projection available from patterns.
	mirrorPayload, err := NewPatternMirrorPayload(customID, "", true, PatternMemory{
		Content: content, Source: "auto_learn", Category: category, PRNumber: 41,
		Extra: map[string]string{"repo": originRepo},
	})
	if err != nil {
		t.Fatal(err)
	}
	outbox := &fakeMirrorOutbox{events: []store.MemoryMirrorOutboxEvent{{
		ID: 1, InstallationID: install, AggregateType: store.MemoryMirrorPattern,
		AggregateID: 101, Operation: store.MemoryMirrorUpsert, Payload: mirrorPayload,
	}}}
	worker := NewMirrorWorker(outbox, func(context.Context, int64) MirrorIndexer { return base }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	processed, err := worker.RunOnce(ctx, 1)
	if err != nil {
		t.Fatalf("mirror replay: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed=%d, want 1", processed)
	}

	after := readRow(t, pool, install, customID)
	afterReview := readReviewID(t, pool, install, customID)
	if beforeReview == nil || afterReview == nil || *afterReview != *beforeReview {
		t.Fatalf("review provenance changed: before=%v after=%v", beforeReview, afterReview)
	}
	if after.metadata["score"] != "97" || after.metadata["origin_stage"] != "scoring" || after.metadata["repo"] != originRepo {
		t.Fatalf("rich pipeline metadata was reduced: before=%v after=%v", before.metadata, after.metadata)
	}
	if after.content != before.content || after.containerTag != before.containerTag || after.docType != before.docType {
		t.Fatalf("live projection changed: before=%+v after=%+v", before, after)
	}
	if embedder.calls != beforeEmbedCalls {
		t.Fatalf("mirror embedded an already-live document: calls before=%d after=%d", beforeEmbedCalls, embedder.calls)
	}

	// Exercise the atomic conflict guard directly: this is the state reached if
	// the pipeline inserts after mirrorDoc's preflight read but before its write.
	reducedDoc, err := buildSharedPatternDoc(PatternMemory{
		Content: content, CustomID: customID, Source: "auto_learn", Category: category, PRNumber: 41,
		Extra: map[string]string{"repo": originRepo},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := base.writeDocs(ctx, []Doc{reducedDoc}, true); err != nil {
		t.Fatalf("raced mirror write: %v", err)
	}
	afterRace := readRow(t, pool, install, customID)
	afterRaceReview := readReviewID(t, pool, install, customID)
	if afterRaceReview == nil || *afterRaceReview != reviewID || afterRace.metadata["score"] != "97" ||
		afterRace.metadata["origin_stage"] != "scoring" || afterRace.metadata["repo"] != originRepo {
		t.Fatalf("atomic mirror conflict reduced live row: review=%v metadata=%v", afterRaceReview, afterRace.metadata)
	}
}

// Two relational pattern rows may intentionally converge on the same
// deterministic memory custom ID. Removing either projection must not remove
// the shared memory while the other projection remains authoritative.
func TestMirrorWorkerDeleteKeepsMemoryOwnedByDuplicatePattern(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	lockMirrorOutboxPGTests(t, pool, ctx)
	st := store.NewWithDB(pool)
	if _, err := pool.Exec(ctx, `DELETE FROM memory_mirror_outbox`); err != nil {
		t.Fatalf("clear mirror outbox: %v", err)
	}
	idx := NewPGIndexer(pool, nil, install, pgTestDims, slog.New(slog.DiscardHandler))

	var repoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, (random() * 1000000000)::bigint, 'acme/mirror-owner-duplicate')
		RETURNING id`, install).Scan(&repoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	source := "manual"
	customID := PatternCustomID("", "mirror-owner-duplicate", source, "guard shared writes")
	first, err := st.CreatePattern(ctx, install, &repoID, "guard shared writes", nil, nil, &source, nil, nil, stringPointer(customID), nil)
	if err != nil {
		t.Fatalf("create first pattern: %v", err)
	}
	second, err := st.CreatePattern(ctx, install, &repoID, "guard shared writes", nil, nil, &source, nil, nil, stringPointer(customID), nil)
	if err != nil {
		t.Fatalf("create duplicate pattern: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_type='pattern' AND aggregate_id=ANY($2::bigint[])`, install, []int64{first.ID, second.ID})
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE id=ANY($1::bigint[])`, []int64{first.ID, second.ID})
		_, _ = pool.Exec(bg, `DELETE FROM repos WHERE id=$1`, repoID)
	})

	if _, err := idx.IndexPattern(ctx, "mirror-owner-duplicate", PatternMemory{
		Content: "guard shared writes", CustomID: customID, Source: source,
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	if err := st.DeletePattern(ctx, first.ID, []int64{install}); err != nil {
		t.Fatalf("delete first pattern: %v", err)
	}

	worker := NewMirrorWorker(st, func(context.Context, int64) MirrorIndexer { return idx }, slog.New(slog.DiscardHandler))
	for {
		processed, err := worker.RunOnce(ctx, 50)
		if err != nil {
			t.Fatalf("drain mirror events: %v", err)
		}
		if processed == 0 {
			break
		}
	}
	if row := readRow(t, pool, install, customID); row.deletedAt != nil {
		t.Fatal("deleting one duplicate pattern tombstoned memory still owned by the other pattern")
	}
}

func stringPointer(value string) *string { return &value }

// An old aggregate's delete and a recreated aggregate's upsert share memory
// identity even though their relational IDs differ. Concurrent machines must
// not claim both transitions: otherwise the delete can finish after the newer
// upsert and leave the converged memory tombstoned.
func TestMirrorOutboxSerializesDeleteAndRecreateClaimsByCustomID(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	lockMirrorOutboxPGTests(t, pool, ctx)
	st := store.NewWithDB(pool)
	if _, err := pool.Exec(ctx, `DELETE FROM memory_mirror_outbox`); err != nil {
		t.Fatalf("clear mirror outbox: %v", err)
	}
	customID := PatternCustomID("", "mirror-owner-recreate", "manual", "guard recreated writes")

	oldPayload, err := NewDeleteMirrorPayload(customID)
	if err != nil {
		t.Fatal(err)
	}
	newPayload, err := NewPatternMirrorPayload(customID, "mirror-owner-recreate", false, PatternMemory{
		Content: "guard recreated writes", Source: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	oldID := time.Now().UnixNano()
	newID := oldID + 1
	for _, event := range []store.MemoryMirrorEvent{
		{InstallationID: install, AggregateType: store.MemoryMirrorPattern, AggregateID: oldID, Operation: store.MemoryMirrorDelete, Payload: oldPayload},
		{InstallationID: install, AggregateType: store.MemoryMirrorPattern, AggregateID: newID, Operation: store.MemoryMirrorUpsert, Payload: newPayload},
	} {
		if err := st.WithMemoryMirrorTx(ctx, func(pgx.Tx) (store.MemoryMirrorEvent, error) { return event, nil }); err != nil {
			t.Fatalf("enqueue event: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_id=ANY($2::bigint[])`, install, []int64{oldID, newID})
	})

	first, err := st.ClaimMemoryMirrorEvents(ctx, 1, time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("first machine claim=%v err=%v", first, err)
	}
	secondMachine := store.NewWithDB(pool)
	second, err := secondMachine.ClaimMemoryMirrorEvents(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("second machine claim: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second machine concurrently claimed same custom ID: %+v", second)
	}
}

// A later legacy event has no persisted custom ID until its first worker pass.
// It must therefore wait behind every earlier pattern transition in the tenant:
// that earlier event may target the same ID the legacy payload reconstructs.
func TestMirrorOutboxBoundPatternBlocksLaterLegacyUnknownClaim(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	lockMirrorOutboxPGTests(t, pool, ctx)
	st := store.NewWithDB(pool)
	if _, err := pool.Exec(ctx, `DELETE FROM memory_mirror_outbox`); err != nil {
		t.Fatalf("clear mirror outbox: %v", err)
	}

	const (
		repo    = "mirror-owner-legacy-order"
		content = "legacy ordered guard writes"
	)
	customID := PatternCustomID("", repo, "learned", content)
	boundPayload, err := NewPatternMirrorPayload(customID, repo, false, PatternMemory{
		Content: content, Source: "auto_learn",
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyPayload := json.RawMessage(`{
		"repo":"mirror-owner-legacy-order",
		"pattern":{"Content":"legacy ordered guard writes","Source":"auto_learn"}
	}`)
	firstAggregateID := install*1_000_000 + 2_541
	legacyAggregateID := firstAggregateID + 1
	for _, event := range []store.MemoryMirrorEvent{
		{InstallationID: install, AggregateType: store.MemoryMirrorPattern, AggregateID: firstAggregateID, Operation: store.MemoryMirrorUpsert, Payload: boundPayload},
		{InstallationID: install, AggregateType: store.MemoryMirrorPattern, AggregateID: legacyAggregateID, Operation: store.MemoryMirrorDelete, Payload: legacyPayload},
	} {
		if err := st.WithMemoryMirrorTx(ctx, func(pgx.Tx) (store.MemoryMirrorEvent, error) { return event, nil }); err != nil {
			t.Fatalf("enqueue event: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_id=ANY($2::bigint[])`, install, []int64{firstAggregateID, legacyAggregateID})
	})

	first, err := st.ClaimMemoryMirrorEvents(ctx, 1, time.Minute)
	if err != nil || len(first) != 1 || first[0].AggregateID != firstAggregateID {
		t.Fatalf("first machine claim=%v err=%v", first, err)
	}
	secondMachine := store.NewWithDB(pool)
	second, err := secondMachine.ClaimMemoryMirrorEvents(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("second machine claim: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("later legacy unknown event bypassed earlier bound pattern: %+v", second)
	}
}

// The authority check also covers rows predating both memory identity columns.
// Their custom ID is reconstructed by the same memory identity code used for
// tombstone replay, rather than treating NULL as "not an owner".
func TestMirrorWorkerDeleteKeepsMemoryOwnedByLegacyDuplicatePattern(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	lockMirrorOutboxPGTests(t, pool, ctx)
	st := store.NewWithDB(pool)
	if _, err := pool.Exec(ctx, `DELETE FROM memory_mirror_outbox`); err != nil {
		t.Fatalf("clear mirror outbox: %v", err)
	}
	idx := NewPGIndexer(pool, nil, install, pgTestDims, slog.New(slog.DiscardHandler))

	var repoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, (random() * 1000000000)::bigint, 'acme/mirror-owner-legacy')
		RETURNING id`, install).Scan(&repoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	const content = "legacy duplicate guard writes"
	ids := make([]int64, 2)
	for i := range ids {
		if err := pool.QueryRow(ctx, `
			INSERT INTO patterns (installation_id, repo_id, content, source, category, memory_doc_id, memory_custom_id)
			VALUES ($1, $2, $3, 'auto_learn', 'correctness', NULL, NULL)
			RETURNING id`, install, repoID, content).Scan(&ids[i]); err != nil {
			t.Fatalf("seed legacy pattern %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_id=ANY($2::bigint[])`, install, ids)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE id=ANY($1::bigint[])`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM repos WHERE id=$1`, repoID)
	})

	customID := PatternCustomID("", "mirror-owner-legacy", "learned", content)
	if _, err := idx.IndexPattern(ctx, "mirror-owner-legacy", PatternMemory{
		Content: content, CustomID: customID, Source: "auto_learn", Category: "correctness",
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	if err := st.DeletePattern(ctx, ids[0], []int64{install}); err != nil {
		t.Fatalf("delete first legacy pattern: %v", err)
	}

	worker := NewMirrorWorker(st, func(context.Context, int64) MirrorIndexer { return idx }, slog.New(slog.DiscardHandler))
	if processed, err := worker.RunOnce(ctx, 1); err != nil || processed != 1 {
		t.Fatalf("process legacy delete: processed=%d err=%v", processed, err)
	}
	if row := readRow(t, pool, install, customID); row.deletedAt != nil {
		t.Fatal("legacy duplicate owner did not prevent memory tombstone")
	}
	var boundCustomID string
	if err := pool.QueryRow(ctx, `
		SELECT payload->>'custom_id' FROM memory_mirror_outbox
		WHERE installation_id=$1 AND aggregate_type='pattern' AND aggregate_id=$2
		ORDER BY id DESC LIMIT 1`, install, ids[0]).Scan(&boundCustomID); err != nil {
		t.Fatalf("read bound legacy identity: %v", err)
	}
	if boundCustomID != customID {
		t.Fatalf("bound legacy custom ID=%q want=%q", boundCustomID, customID)
	}
}

// A recreated pattern cannot commit between delete authority and the external
// tombstone. Both producer and worker hold the same tenant/custom-ID advisory
// lock, so the recreate waits; its later outbox upsert then deterministically
// resurrects the memory.
func TestMirrorDeleteAndRecreateSerializeTheAuthorityGap(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	lockMirrorOutboxPGTests(t, pool, ctx)
	st := store.NewWithDB(pool)
	if _, err := pool.Exec(ctx, `DELETE FROM memory_mirror_outbox`); err != nil {
		t.Fatalf("clear mirror outbox: %v", err)
	}
	idx := NewPGIndexer(pool, nil, install, pgTestDims, slog.New(slog.DiscardHandler))

	var repoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, (random() * 1000000000)::bigint, 'acme/mirror-owner-gap')
		RETURNING id`, install).Scan(&repoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	source := "manual"
	customID := PatternCustomID("", "mirror-owner-gap", source, "guard authority gap")
	oldPattern, err := st.CreatePattern(ctx, install, &repoID, "guard authority gap", nil, nil, &source, nil, nil, stringPointer(customID), nil)
	if err != nil {
		t.Fatalf("create old pattern: %v", err)
	}
	var recreatedPatternID int64
	t.Cleanup(func() {
		bg := context.Background()
		ids := []int64{oldPattern.ID}
		if recreatedPatternID != 0 {
			ids = append(ids, recreatedPatternID)
		}
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id=$1 AND aggregate_id=ANY($2::bigint[])`, install, ids)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE id=ANY($1::bigint[])`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM repos WHERE id=$1`, repoID)
	})
	first, err := st.ClaimMemoryMirrorEvents(ctx, 1, time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("claim initial upsert=%v err=%v", first, err)
	}
	if err := st.MarkMemoryMirrorEventProcessed(ctx, first[0]); err != nil {
		t.Fatalf("finish initial upsert: %v", err)
	}
	if _, err := idx.IndexPattern(ctx, "mirror-owner-gap", PatternMemory{Content: "guard authority gap", CustomID: customID, Source: source}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	if err := st.DeletePattern(ctx, oldPattern.ID, []int64{install}); err != nil {
		t.Fatalf("delete old pattern: %v", err)
	}
	claimed, err := st.ClaimMemoryMirrorEvents(ctx, 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].Operation != store.MemoryMirrorDelete {
		t.Fatalf("claim delete=%v err=%v", claimed, err)
	}

	enteredDelete := make(chan struct{})
	releaseDelete := make(chan struct{})
	processedDelete := make(chan error, 1)
	go func() {
		processedDelete <- st.ProcessMemoryMirrorEvent(ctx, claimed[0], customID, nil, func(ctx context.Context, authorized bool) error {
			if !authorized {
				return fmt.Errorf("delete unexpectedly unauthorized")
			}
			close(enteredDelete)
			<-releaseDelete
			return idx.DeleteDocument(ctx, customID)
		})
	}()
	select {
	case <-enteredDelete:
	case <-time.After(5 * time.Second):
		t.Fatal("delete never entered external operation")
	}

	type createResult struct {
		pattern *store.Pattern
		err     error
	}
	created := make(chan createResult, 1)
	go func() {
		pattern, err := st.CreatePattern(ctx, install, &repoID, "guard authority gap", nil, nil, &source, nil, nil, stringPointer(customID), nil)
		created <- createResult{pattern: pattern, err: err}
	}()
	select {
	case result := <-created:
		if result.pattern != nil {
			recreatedPatternID = result.pattern.ID
		}
		close(releaseDelete)
		<-processedDelete
		t.Fatalf("recreate committed inside authority-to-delete gap: pattern=%v err=%v", result.pattern, result.err)
	case <-time.After(150 * time.Millisecond):
	}
	close(releaseDelete)
	if err := <-processedDelete; err != nil {
		t.Fatalf("process delete: %v", err)
	}
	result := <-created
	if result.err != nil {
		t.Fatalf("recreate pattern: %v", result.err)
	}
	if result.pattern == nil {
		t.Fatal("recreate returned no pattern")
	}
	recreatedPatternID = result.pattern.ID

	if row := readRow(t, pool, install, customID); row.deletedAt == nil {
		t.Fatal("test did not observe the old delete tombstone before recreate replay")
	}
	worker := NewMirrorWorker(st, func(context.Context, int64) MirrorIndexer { return idx }, slog.New(slog.DiscardHandler))
	if processed, err := worker.RunOnce(ctx, 1); err != nil || processed != 1 {
		t.Fatalf("process recreated upsert: processed=%d err=%v", processed, err)
	}
	if row := readRow(t, pool, install, customID); row.deletedAt != nil {
		t.Fatal("recreated pattern's later upsert did not win")
	}
}
