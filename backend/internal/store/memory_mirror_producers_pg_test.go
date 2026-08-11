package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/jackc/pgx/v5"
)

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
	requireMirrorOutbox(t, pool, ctx)
	st := &Store{Pool: pool, Q: db.New(pool)}
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
	pattern, err := st.CreatePattern(ctx, installationID, &repoID, "guard writes", nil, nil, &source, nil, nil, &customID)
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
	if events[2].typ != "rule" || string(events[2].payload) != `{"custom_id":"rule--`+fmt.Sprint(rule.ID)+`"}` {
		t.Fatalf("disabled payload=%s", events[2].payload)
	}
}

func TestWithMemoryMirrorTxRollsBackMutationWhenEnqueueValidationFails(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
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
