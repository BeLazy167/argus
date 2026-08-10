package store

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// The merge semantics live in SQL (jsonb ||), so they can only be verified
// against a real Postgres. That is a deliberate trade: a Go-side
// read-modify-write would be unit-testable but not atomic, and atomicity is
// the property that matters — the settings form and an operator's direct
// UPDATE write the same column concurrently.
//
// Gated on TEST_DATABASE_URL, never the app's DATABASE_URL.
func mergeTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

// seedInstallation creates a throwaway installation row and returns its id.
func seedInstallation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, flags string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login, feature_flags)
		VALUES ((random() * 1000000000)::bigint, 'merge-test', $1::jsonb)
		RETURNING id`, flags).Scan(&id)
	if err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM installations WHERE id = $1`, id)
	})
	return id
}

func readFlags(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) map[string]any {
	t.Helper()
	var raw json.RawMessage
	if err := pool.QueryRow(ctx, `SELECT feature_flags FROM installations WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatalf("read flags: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal flags %s: %v", raw, err)
	}
	return out
}

// TestMergeInstallationFeatureFlagsPreservesForeignKeys: the settings form owns
// three keys; every other key in the column belongs to an operator and must
// survive a save. Before the merge, saving the form wrote its three-field
// struct over the whole column, so toggling cross-PR checks silently cleared
// every operator-set flag.
func TestMergeInstallationFeatureFlagsPreservesForeignKeys(t *testing.T) {
	pool, ctx := mergeTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	id := seedInstallation(t, ctx, pool,
		`{"operator_only_flag":"set","cross_pr_checks":false,"some_future_flag":{"nested":1}}`)

	patch := json.RawMessage(`{"issue_acceptance":true,"cross_pr_checks":true,"max_linked_prs":12}`)
	if err := st.MergeInstallationFeatureFlags(ctx, id, patch); err != nil {
		t.Fatalf("merge: %v", err)
	}

	got := readFlags(t, ctx, pool, id)
	if got["operator_only_flag"] != "set" {
		t.Errorf("operator_only_flag = %v; a settings save cleared an operator-set flag", got["operator_only_flag"])
	}
	if _, ok := got["some_future_flag"]; !ok {
		t.Errorf("unknown operator key dropped by a settings save: %v", got)
	}
	if got["cross_pr_checks"] != true || got["issue_acceptance"] != true {
		t.Errorf("UI-owned keys not written: %v", got)
	}
	if got["max_linked_prs"] != float64(12) {
		t.Errorf("max_linked_prs = %v, want 12", got["max_linked_prs"])
	}
}

// TestMergeInstallationFeatureFlagsWritesFalse: booleans must round-trip their
// zero value, or the toggles become one-way — settable but never clearable.
func TestMergeInstallationFeatureFlagsWritesFalse(t *testing.T) {
	pool, ctx := mergeTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	id := seedInstallation(t, ctx, pool, `{"issue_acceptance":true,"cross_pr_checks":true,"operator_only_flag":"set"}`)

	patch := json.RawMessage(`{"issue_acceptance":false,"cross_pr_checks":false,"max_linked_prs":5}`)
	if err := st.MergeInstallationFeatureFlags(ctx, id, patch); err != nil {
		t.Fatalf("merge: %v", err)
	}
	got := readFlags(t, ctx, pool, id)
	if got["issue_acceptance"] != false || got["cross_pr_checks"] != false {
		t.Errorf("toggles did not clear: %v", got)
	}
	if got["operator_only_flag"] != "set" {
		t.Errorf("operator_only_flag = %v, want preserved", got["operator_only_flag"])
	}
}

// TestMergeInstallationFeatureFlagsIsAtomic is the reason the merge is SQL
// rather than Go. A read-modify-write loses one of two concurrent writers:
// an operator sets a flag by direct UPDATE while users may be saving settings,
// and a lost update reverts that change silently, with an audit entry naming
// only the three toggles.
//
// Interleaves N settings-style merges against N operator flips and asserts
// both survive: the operator's key is present, and the last toggle write took.
func TestMergeInstallationFeatureFlagsIsAtomic(t *testing.T) {
	pool, ctx := mergeTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}
	id := seedInstallation(t, ctx, pool, `{}`)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(2 * n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_ = st.MergeInstallationFeatureFlags(ctx, id,
				json.RawMessage(`{"issue_acceptance":true,"cross_pr_checks":true,"max_linked_prs":7}`))
		}()
		go func() {
			defer wg.Done()
			// The operator's flip, exactly as the phase-5 runbook writes it.
			_, _ = pool.Exec(ctx,
				`UPDATE installations SET feature_flags = feature_flags || '{"operator_only_flag":"set"}'::jsonb WHERE id = $1`, id)
		}()
	}
	wg.Wait()

	got := readFlags(t, ctx, pool, id)
	if got["operator_only_flag"] != "set" {
		t.Errorf("operator_only_flag = %v after %d interleaved writers; the write was lost", got["operator_only_flag"], n)
	}
	if got["issue_acceptance"] != true || got["max_linked_prs"] != float64(7) {
		t.Errorf("settings keys lost to interleaving: %v", got)
	}
}

// TestPatternIDLookupsExcludeOtherTenants is the negative half of the tenant
// scope. The Go-side test proves the installation id REACHES these queries;
// only a database can prove it EXCLUDES. That distinction matters here because
// the ids stopped being globally unique: PGIndexer returns a deterministic
// customId, so two installations that learned the same pattern in same-named
// repos hold the identical string, and the pre-fix `LIMIT 1` with no ORDER BY
// would resolve one tenant's search hit to whichever row Postgres returned.
func TestPatternIDLookupsExcludeOtherTenants(t *testing.T) {
	pool, ctx := mergeTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}

	a := seedInstallation(t, ctx, pool, `{}`)
	b := seedInstallation(t, ctx, pool, `{}`)

	// The same deterministic id under two installations — ordinary, because
	// the id is derived from content rather than assigned globally.
	const shared = "api--confirmed--92d0d4e51341"
	var idA, idB int64
	for _, seed := range []struct {
		install int64
		out     *int64
	}{{a, &idA}, {b, &idB}} {
		err := pool.QueryRow(ctx, `
			INSERT INTO patterns (installation_id, content, source, memory_doc_id, memory_custom_id)
			VALUES ($1, 'shared content', 'confirmed', $2, $2)
			RETURNING id`, seed.install, shared).Scan(seed.out)
		if err != nil {
			t.Fatalf("seed pattern for install %d: %v", seed.install, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM patterns WHERE id = $1`, *seed.out) })
	}

	for name, lookup := range map[string]func(context.Context, int64, string) (int64, error){
		"by memory_doc_id": st.GetPatternIDByMemoryDocID,
		"by custom_id":     st.GetPatternIDByCustomID,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := lookup(ctx, a, shared)
			if err != nil {
				t.Fatalf("lookup for install A: %v", err)
			}
			if got != idA {
				t.Errorf("install A resolved to pattern %d, want its own %d (install B holds %d) — cross-tenant attribution", got, idA, idB)
			}
			got, err = lookup(ctx, b, shared)
			if err != nil {
				t.Fatalf("lookup for install B: %v", err)
			}
			if got != idB {
				t.Errorf("install B resolved to pattern %d, want its own %d", got, idB)
			}
		})
	}
}
