package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgDesiredSpaceResolver keeps the test's active model in PostgreSQL. Separate
// EmbedderRegistry values use it like separate app machines resolving the same
// committed provider configuration.
type pgDesiredSpaceResolver struct {
	pool    *pgxpool.Pool
	baseURL string
}

func (r *pgDesiredSpaceResolver) ResolveEmbeddingsKey(ctx context.Context, installationID int64) (string, string, string, bool, error) {
	return r.resolve(ctx, r.pool, installationID)
}

func (r *pgDesiredSpaceResolver) ResolveEmbeddingsKeyFromConn(ctx context.Context, conn *pgxpool.Conn, installationID int64) (string, string, string, bool, error) {
	return r.resolve(ctx, conn, installationID)
}

type pgDesiredSpaceQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (r *pgDesiredSpaceResolver) resolve(ctx context.Context, q pgDesiredSpaceQuerier, installationID int64) (string, string, string, bool, error) {
	var model string
	err := q.QueryRow(ctx, `
		SELECT COALESCE(default_settings->>'test_embedding_model', '')
		FROM installations WHERE id = $1`, installationID).Scan(&model)
	return "", r.baseURL, model, true, err
}

type reembedResult struct {
	repaired int
	err      error
}

func TestRegistryOverlappingEmbeddingRotationsConvergeToLatestSpace(t *testing.T) {
	pool, installationID := pgTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	bEntered := make(chan struct{})
	releaseB := make(chan struct{})
	var enterBOnce, releaseBOnce sync.Once
	releaseBlockedB := func() { releaseBOnce.Do(func() { close(releaseB) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Model == "space-B" {
			enterBOnce.Do(func() { close(bEntered) })
			select {
			case <-releaseB:
			case <-r.Context().Done():
				return
			}
		}
		data := make([]map[string]any, len(req.Input))
		for i, text := range req.Input {
			vec := make([]float32, StorageDimensions)
			vec[0] = float32(len(text) + 1)
			data[i] = map[string]any{"index": i, "embedding": vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	defer releaseBlockedB()

	setDesired := func(model string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			UPDATE installations
			SET default_settings = jsonb_set(COALESCE(default_settings, '{}'::jsonb),
				'{test_embedding_model}', to_jsonb($2::text), true)
			WHERE id = $1`, installationID, model); err != nil {
			t.Fatalf("set desired embedding space %s: %v", model, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			UPDATE installations SET default_settings = COALESCE(default_settings, '{}'::jsonb) - 'test_embedding_model'
			WHERE id = $1`, installationID)
	})

	newMachine := func() *Registry {
		t.Helper()
		embedders := NewEmbedderRegistry(
			&pgDesiredSpaceResolver{pool: pool, baseURL: server.URL},
			PlatformEmbeddings{Dimensions: StorageDimensions},
			discardLogger(),
		)
		return NewRegistry(discardLogger()).WithPostgresBackend(pool, embedders)
	}

	// The live corpus starts fully embedded in A.
	setDesired("space-A")
	machineA := newMachine()
	seed := machineA.GetIndexer(ctx, installationID)
	if seed == nil {
		t.Fatal("memory registry did not resolve the test embedder")
	}
	if err := seed.(*PGIndexer).ImportDocs(ctx, []Doc{lifecycleDoc("rotation-probe", "latest space wins")}); err != nil {
		t.Fatalf("seed space A: %v", err)
	}

	// Machine B captures B and holds the tenant advisory lock while its provider
	// call is paused. C commits on PostgreSQL and a second machine asks for the
	// same repair before B can finish.
	setDesired("space-B")
	machineB := newMachine()
	bDone := make(chan reembedResult, 1)
	go func() {
		n, err := machineB.ReembedCurrentSpace(ctx, installationID, 10)
		bDone <- reembedResult{repaired: n, err: err}
	}()
	select {
	case <-bEntered:
	case <-ctx.Done():
		t.Fatalf("space B repair did not enter the embedding call: %v", ctx.Err())
	}

	setDesired("space-C")
	machineC := newMachine()
	cDone := make(chan reembedResult, 1)
	go func() {
		n, err := machineC.ReembedCurrentSpace(ctx, installationID, 10)
		cDone <- reembedResult{repaired: n, err: err}
	}()

	// The old implementation treated a failed pg_try_advisory_lock as success;
	// prove the second machine does not silently drop C while B owns the lock.
	var cEarly *reembedResult
	select {
	case result := <-cDone:
		cEarly = &result
		t.Errorf("space C repair returned while B still held the advisory lock: repaired=%d err=%v", result.repaired, result.err)
	case <-time.After(150 * time.Millisecond):
	}
	releaseBlockedB()

	waitResult := func(name string, done <-chan reembedResult, early *reembedResult) {
		t.Helper()
		var result reembedResult
		if early != nil {
			result = *early
		} else {
			select {
			case result = <-done:
			case <-ctx.Done():
				t.Fatalf("machine %s repair did not finish: %v", name, ctx.Err())
			}
		}
		if result.err != nil {
			t.Errorf("machine %s repair: %v (repaired=%d)", name, result.err, result.repaired)
		}
	}
	waitResult("B", bDone, nil)
	waitResult("C", cDone, cEarly)

	wantSpace := NewEmbedder("", server.URL, "space-C", StorageDimensions).SpaceID()
	var gotSpaces []string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(array_agg(DISTINCT embedding_space), ARRAY[]::text[])
		FROM live_memories WHERE installation_id = $1`, installationID).Scan(&gotSpaces); err != nil {
		t.Fatalf("read final corpus spaces: %v", err)
	}
	if len(gotSpaces) != 1 || gotSpaces[0] != wantSpace {
		t.Fatalf("final live corpus spaces = %v, want canonical C space %q", gotSpaces, wantSpace)
	}

	pending, err := NewPGIndexer(pool, NewEmbedder("", server.URL, "space-C", StorageDimensions),
		installationID, StorageDimensions, discardLogger()).CountReembedPending(ctx)
	if err != nil {
		t.Fatalf("count final pending rows: %v", err)
	}
	if pending != 0 {
		t.Fatalf("final corpus has %d row(s) outside C", pending)
	}
}

func TestRegistryCapturedIndexerCannotWriteObsoleteSpaceAfterRepair(t *testing.T) {
	pool, installationID := pgTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data := make([]map[string]any, len(req.Input))
		for i, text := range req.Input {
			vec := make([]float32, StorageDimensions)
			vec[0] = float32(len(text) + 1)
			data[i] = map[string]any{"index": i, "embedding": vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()

	setDesired := func(model string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			UPDATE installations
			SET default_settings = jsonb_set(COALESCE(default_settings, '{}'::jsonb),
				'{test_embedding_model}', to_jsonb($2::text), true)
			WHERE id = $1`, installationID, model); err != nil {
			t.Fatalf("set desired embedding space %s: %v", model, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			UPDATE installations SET default_settings = COALESCE(default_settings, '{}'::jsonb) - 'test_embedding_model'
			WHERE id = $1`, installationID)
	})

	newMachine := func() *Registry {
		embedders := NewEmbedderRegistry(
			&pgDesiredSpaceResolver{pool: pool, baseURL: server.URL},
			PlatformEmbeddings{Dimensions: StorageDimensions},
			discardLogger(),
		)
		return NewRegistry(discardLogger()).WithPostgresBackend(pool, embedders)
	}

	setDesired("space-A")
	machineA := newMachine()
	capturedA := machineA.GetIndexer(ctx, installationID)
	if capturedA == nil {
		t.Fatal("machine A did not resolve an indexer")
	}
	if err := capturedA.(*PGIndexer).ImportDocs(ctx, []Doc{
		lifecycleDoc("before-rotation", "memory written before rotation"),
	}); err != nil {
		t.Fatalf("seed space A: %v", err)
	}

	setDesired("space-B")
	machineB := newMachine()
	if repaired, err := machineB.ReembedCurrentSpace(ctx, installationID, 10); err != nil {
		t.Fatalf("repair space B: %v (repaired=%d)", err, repaired)
	}

	// This is the late PipelineRun/multi-machine writer: it was handed an A
	// indexer before rotation and writes only after B repair reported success.
	if err := capturedA.(*PGIndexer).ImportDocs(ctx, []Doc{
		lifecycleDoc("late-writer", "late writer survives dense retrieval"),
	}); err != nil {
		t.Fatalf("late captured-A write: %v", err)
	}

	wantSpace := NewEmbedder("", server.URL, "space-B", StorageDimensions).SpaceID()
	var gotSpaces []string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(array_agg(DISTINCT embedding_space), ARRAY[]::text[])
		FROM live_memories WHERE installation_id = $1`, installationID).Scan(&gotSpaces); err != nil {
		t.Fatalf("read final corpus spaces: %v", err)
	}
	if len(gotSpaces) != 1 || gotSpaces[0] != wantSpace {
		t.Fatalf("final live corpus spaces = %v, want only B %q", gotSpaces, wantSpace)
	}

	current := machineB.GetIndexer(ctx, installationID)
	matches, err := current.Search(ctx, MemoryQuery{
		Query: "late writer survives dense retrieval", Repo: "api", Scope: ScopeRepo,
		Type: TypePattern, Limit: 10, Threshold: 0.9,
	})
	if err != nil {
		t.Fatalf("search current B space: %v", err)
	}
	found := false
	for _, match := range matches {
		if match.ID == "late-writer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("late writer is excluded from B dense retrieval: matches=%+v", matches)
	}

	pending, err := current.(*PGIndexer).CountReembedPending(ctx)
	if err != nil {
		t.Fatalf("count final pending rows: %v", err)
	}
	if pending != 0 {
		t.Fatalf("final corpus has %d row(s) outside B after late write", pending)
	}
}
