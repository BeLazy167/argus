package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
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

func requireMirrorOutbox(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, ctx context.Context) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('memory_mirror_outbox') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Skip("migration 075 is supplied by the integration branch")
	}
}

func TestPatternAndRuleMutationsEnqueueOrderedMirrorEvents(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	lockMirrorOutboxPGTests(t, pool, ctx)
	requireMirrorOutbox(t, pool, ctx)
	st := &Store{Pool: pool, q: db.New(pool)}
	installationID, _, _ := seedLearnTenant(t, ctx, pool, "mirror-producers")
	var repoID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM repos WHERE installation_id=$1`, installationID).Scan(&repoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM memory_mirror_outbox WHERE installation_id=$1`, installationID)
	})

	customID := "mirror-producers--manual--abc"
	source := "manual"
	pattern, err := st.CreatePattern(ctx, installationID, &repoID, "guard writes", nil, nil, &source, nil, nil, &customID, map[string]string{"repo": "acme/mirror-producers"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := st.CreateRule(ctx, installationID, "safety", "never panic", 5, true)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err = st.UpdateRule(ctx, rule.ID, []int64{installationID}, nil, nil, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if _, err = st.UpdateRule(ctx, rule.ID, []int64{installationID}, nil, nil, nil, &enabled); err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteRule(ctx, rule.ID, []int64{installationID}); err != nil {
		t.Fatal(err)
	}
	if err = st.DeletePattern(ctx, pattern.ID, []int64{installationID}); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `SELECT aggregate_type,aggregate_id,operation,payload FROM memory_mirror_outbox WHERE installation_id=$1 ORDER BY id`, installationID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type event struct {
		typ     string
		id      int64
		op      string
		payload json.RawMessage
	}
	var events []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.typ, &e.id, &e.op, &e.payload); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	wantOps := []string{"upsert", "upsert", "delete", "upsert", "delete", "delete"}
	if len(events) != len(wantOps) {
		t.Fatalf("events=%+v", events)
	}
	for i, want := range wantOps {
		if events[i].op != want {
			t.Errorf("event %d op=%q want=%q", i, events[i].op, want)
		}
	}
	if events[0].typ != "pattern" || events[0].id != pattern.ID || events[5].typ != "pattern" {
		t.Fatalf("pattern events=%+v", events)
	}
	var patternPayload struct {
		Pattern struct {
			Extra map[string]string
		} `json:"pattern"`
	}
	if err := json.Unmarshal(events[0].payload, &patternPayload); err != nil {
		t.Fatal(err)
	}
	if patternPayload.Pattern.Extra["repo"] != "acme/mirror-producers" {
		t.Fatalf("pattern mirror lost full origin repo: %s", events[0].payload)
	}
	var disabledPayload map[string]string
	if err := json.Unmarshal(events[2].payload, &disabledPayload); err != nil {
		t.Fatal(err)
	}
	if events[2].typ != "rule" || disabledPayload["custom_id"] != "rule--"+fmt.Sprint(rule.ID) {
		t.Fatalf("disabled payload=%s", events[2].payload)
	}
}

func TestWithMemoryMirrorTxRollsBackMutationWhenEnqueueValidationFails(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	lockMirrorOutboxPGTests(t, pool, ctx)
	st := &Store{Pool: pool, q: db.New(pool)}
	installationID, _, _ := seedLearnTenant(t, ctx, pool, "mirror-rollback")
	err := st.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		if _, err := tx.Exec(ctx, `INSERT INTO rules (installation_id,category,content) VALUES ($1,'test','must roll back')`, installationID); err != nil {
			return MemoryMirrorEvent{}, err
		}
		return MemoryMirrorEvent{InstallationID: installationID, AggregateType: "invalid", AggregateID: 1, Operation: MemoryMirrorUpsert, Payload: json.RawMessage(`{}`)}, nil
	})
	if err == nil {
		t.Fatal("expected invalid event error")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM rules WHERE installation_id=$1 AND content='must roll back'`, installationID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled-back rules=%d", count)
	}
}

func TestMemoryMirrorAcknowledgementRejectsLostLease(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	lockMirrorOutboxPGTests(t, pool, ctx)
	st := &Store{Pool: pool, q: db.New(pool)}
	installationID, _, _ := seedLearnTenant(t, ctx, pool, "mirror-lost-lease")
	event := MemoryMirrorEvent{
		InstallationID: installationID,
		AggregateType:  MemoryMirrorRule,
		AggregateID:    installationID + 1_000_000_000,
		Operation:      MemoryMirrorDelete,
		Payload:        json.RawMessage(`{"custom_id":"rule--1"}`),
	}
	if err := st.WithMemoryMirrorTx(ctx, func(pgx.Tx) (MemoryMirrorEvent, error) { return event, nil }); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimMemoryMirrorEvents(ctx, 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memory_mirror_outbox SET claimed_at = claimed_at + interval '1 second' WHERE id=$1`, claimed[0].ID); err != nil {
		t.Fatal(err)
	}
	applyCalled := false
	if err := st.ProcessMemoryMirrorEvent(ctx, claimed[0], "rule--1", nil, func(context.Context, bool) error {
		applyCalled = true
		return nil
	}); err == nil {
		t.Fatal("stale worker processed an event after losing its lease")
	}
	if applyCalled {
		t.Fatal("stale worker reached the external operation after losing its lease")
	}
	if err := st.MarkMemoryMirrorEventProcessed(ctx, claimed[0]); err == nil {
		t.Fatal("stale worker acknowledged a reclaimed lease")
	}
}

func TestDeletePatternLegacyNullMemoryIdentityEnqueuesReplayableDelete(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	lockMirrorOutboxPGTests(t, pool, ctx)
	requireMirrorOutbox(t, pool, ctx)
	st := &Store{Pool: pool, q: db.New(pool)}
	installationID, _, _ := seedLearnTenant(t, ctx, pool, "mirror-legacy-delete")
	var repoID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM repos WHERE installation_id=$1`, installationID).Scan(&repoID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM memory_mirror_outbox WHERE installation_id=$1`, installationID)
	})

	var patternID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO patterns (installation_id, repo_id, content, source, category, pr_number, memory_doc_id, memory_custom_id)
		VALUES ($1, $2, 'legacy guard writes', 'auto_learn', 'correctness', 17, NULL, NULL)
		RETURNING id`, installationID, repoID).Scan(&patternID); err != nil {
		t.Fatal(err)
	}

	if err := st.DeletePattern(ctx, patternID, []int64{installationID}); err != nil {
		t.Fatalf("delete legacy pattern: %v", err)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE id=$1`, patternID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("remaining patterns=%d, want 0", remaining)
	}

	var operation string
	var payload struct {
		CustomID string `json:"custom_id"`
		Repo     string `json:"repo"`
		Pattern  struct {
			Content  string
			Source   string
			Category string
			PRNumber int
		} `json:"pattern"`
	}
	if err := pool.QueryRow(ctx, `
		SELECT operation, payload
		FROM memory_mirror_outbox
		WHERE installation_id=$1 AND aggregate_type='pattern' AND aggregate_id=$2
		ORDER BY id DESC LIMIT 1`, installationID, patternID).Scan(&operation, &payload); err != nil {
		t.Fatal(err)
	}
	if operation != MemoryMirrorDelete {
		t.Fatalf("operation=%q, want delete", operation)
	}
	if payload.CustomID != "" || payload.Repo == "" || payload.Pattern.Content != "legacy guard writes" ||
		payload.Pattern.Source != "auto_learn" || payload.Pattern.Category != "correctness" || payload.Pattern.PRNumber != 17 {
		t.Fatalf("delete payload is not replayable: %+v", payload)
	}
}
