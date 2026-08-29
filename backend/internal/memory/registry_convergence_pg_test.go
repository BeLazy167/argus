package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func TestRegistryDuplicateTenantRepairsDoNotStarveAnotherTenant(t *testing.T) {
	pool, firstInstallationID := pgTestPoolWithMaxConns(t, 20)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var secondInstallationID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ($1, $2)
		ON CONFLICT (installation_id) DO UPDATE SET org_login = EXCLUDED.org_login
		RETURNING id`, 910_020, "repair-capacity-neighbour").Scan(&secondInstallationID); err != nil {
		t.Fatalf("create second installation: %v", err)
	}
	installationIDs := []int64{firstInstallationID, secondInstallationID}
	setDesired := func(installationID int64, model string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			UPDATE installations
			SET default_settings = jsonb_set(COALESCE(default_settings, '{}'::jsonb),
				'{test_embedding_model}', to_jsonb($2::text), true)
			WHERE id = $1`, installationID, model); err != nil {
			t.Fatalf("set installation %d desired embedding space %s: %v", installationID, model, err)
		}
	}
	setDesired(firstInstallationID, "space-B")
	setDesired(secondInstallationID, "other-space")
	for i, installationID := range installationIDs {
		if _, err := pool.Exec(ctx, `
			INSERT INTO memories (installation_id, container_tag, custom_id, type, content, metadata)
			VALUES ($1, 'repo:api', $2, 'pattern', $3, '{}'::jsonb)
			ON CONFLICT (installation_id, custom_id) DO UPDATE
			SET content = EXCLUDED.content, embedding = NULL, embedding_model = NULL, embedding_space = NULL,
				deleted_at = NULL, invalidated_at = NULL`,
			installationID, fmt.Sprintf("duplicate-capacity-%d", i), fmt.Sprintf("repair tenant %d", i)); err != nil {
			t.Fatalf("seed installation %d: %v", installationID, err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCleanup()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM memories WHERE installation_id = ANY($1)`, installationIDs); err != nil {
			t.Errorf("cleanup repair memories: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, `
			UPDATE installations
			SET default_settings = COALESCE(default_settings, '{}'::jsonb) - 'test_embedding_model'
			WHERE id = ANY($1)`, installationIDs); err != nil {
			t.Errorf("cleanup repair installations: %v", err)
		}
	})

	bEntered := make(chan struct{})
	otherEntered := make(chan struct{})
	releaseB := make(chan struct{})
	var enterBOnce, enterOtherOnce, releaseBOnce sync.Once
	releaseBlockedB := func() { releaseBOnce.Do(func() { close(releaseB) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch req.Model {
		case "space-B":
			enterBOnce.Do(func() { close(bEntered) })
			select {
			case <-releaseB:
			case <-r.Context().Done():
				return
			}
		case "other-space":
			enterOtherOnce.Do(func() { close(otherEntered) })
		}
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			vec := make([]float32, StorageDimensions)
			vec[0] = 1
			data[i] = map[string]any{"index": i, "embedding": vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	defer releaseBlockedB()

	embedders := NewEmbedderRegistry(
		&pgDesiredSpaceResolver{pool: pool, baseURL: server.URL},
		PlatformEmbeddings{Dimensions: StorageDimensions},
		discardLogger(),
	)
	registry := NewRegistry(discardLogger()).WithPostgresBackend(pool, embedders)

	firstResults := make(chan reembedResult, 10)
	go func() {
		n, err := registry.ReembedCurrentSpace(ctx, firstInstallationID, 10)
		firstResults <- reembedResult{repaired: n, err: err}
	}()
	select {
	case <-bEntered:
	case <-ctx.Done():
		t.Fatalf("first tenant did not enter space B provider call: %v", ctx.Err())
	}

	// MaxConns=20 admits nine repairs. These nine duplicates used to fill the
	// remaining eight permits (with one queued) while repeatedly polling the
	// same advisory lock, preventing an unrelated tenant from starting.
	const duplicateCount = 9
	duplicatesStarted := make(chan struct{}, duplicateCount)
	for i := 0; i < duplicateCount; i++ {
		go func() {
			duplicatesStarted <- struct{}{}
			n, err := registry.ReembedCurrentSpace(ctx, firstInstallationID, 10)
			firstResults <- reembedResult{repaired: n, err: err}
		}()
	}
	for i := 0; i < duplicateCount; i++ {
		<-duplicatesStarted
	}
	time.Sleep(5 * reembedLockPollInterval)

	// A later desired space must not be swallowed while same-installation calls
	// coalesce behind the in-flight repair.
	setDesired(firstInstallationID, "space-C")
	otherDone := make(chan reembedResult, 1)
	go func() {
		n, err := registry.ReembedCurrentSpace(ctx, secondInstallationID, 10)
		otherDone <- reembedResult{repaired: n, err: err}
	}()
	select {
	case <-otherEntered:
	case <-time.After(time.Second):
		t.Fatal("another tenant could not start while duplicate repairs waited")
	}

	releaseBlockedB()
	firstTotal := 0
	for i := 0; i < duplicateCount+1; i++ {
		select {
		case result := <-firstResults:
			if result.err != nil {
				t.Errorf("first-tenant repair failed: %v (repaired=%d)", result.err, result.repaired)
			}
			firstTotal += result.repaired
		case <-ctx.Done():
			t.Fatalf("first-tenant repairs did not finish: %v", ctx.Err())
		}
	}
	if firstTotal != 2 {
		t.Errorf("first tenant repaired %d rows across B then C, want 2", firstTotal)
	}
	select {
	case result := <-otherDone:
		if result.err != nil {
			t.Errorf("other-tenant repair failed: %v (repaired=%d)", result.err, result.repaired)
		} else if result.repaired != 1 {
			t.Errorf("other tenant repaired %d rows, want 1", result.repaired)
		}
	case <-ctx.Done():
		t.Fatalf("other-tenant repair did not finish: %v", ctx.Err())
	}

	wantSpace := NewEmbedder("", server.URL, "space-C", StorageDimensions).SpaceID()
	var gotSpaces []string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(array_agg(DISTINCT embedding_space), ARRAY[]::text[])
		FROM live_memories WHERE installation_id = $1`, firstInstallationID).Scan(&gotSpaces); err != nil {
		t.Fatalf("read first tenant final corpus spaces: %v", err)
	}
	if len(gotSpaces) != 1 || gotSpaces[0] != wantSpace {
		t.Fatalf("first tenant final corpus spaces = %v, want canonical C space %q", gotSpaces, wantSpace)
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

func TestEmbeddingLockWaitersDoNotStarveExclusiveHolderWork(t *testing.T) {
	pool, installationID := pgTestPoolWithMaxConns(t, 2)
	holderCtx, cancelHolder := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHolder()

	bEntered := make(chan struct{})
	releaseB := make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	releaseProvider := func() { releaseOnce.Do(func() { close(releaseB) }) }
	defer releaseProvider()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Model == "space-B" {
			enterOnce.Do(func() { close(bEntered) })
			select {
			case <-releaseB:
			case <-r.Context().Done():
				return
			}
		}
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			vec := make([]float32, StorageDimensions)
			vec[0] = 1
			data[i] = map[string]any{"index": i, "embedding": vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()

	setDesired := func(model string) {
		t.Helper()
		if _, err := pool.Exec(holderCtx, `
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

	embedders := NewEmbedderRegistry(
		&pgDesiredSpaceResolver{pool: pool, baseURL: server.URL},
		PlatformEmbeddings{Dimensions: StorageDimensions},
		discardLogger(),
	)
	registry := NewRegistry(discardLogger()).WithPostgresBackend(pool, embedders)

	setDesired("space-A")
	capturedA := registry.GetIndexer(holderCtx, installationID)
	if capturedA == nil {
		t.Fatal("memory registry did not resolve the test embedder")
	}
	if err := capturedA.(*PGIndexer).ImportDocs(holderCtx, []Doc{
		lifecycleDoc("small-pool-seed", "holder must borrow a work connection"),
	}); err != nil {
		t.Fatalf("seed space A: %v", err)
	}

	setDesired("space-B")
	holderDone := make(chan reembedResult, 1)
	go func() {
		n, err := registry.ReembedCurrentSpace(holderCtx, installationID, 10)
		holderDone <- reembedResult{repaired: n, err: err}
	}()
	select {
	case <-bEntered:
	case <-holderCtx.Done():
		t.Fatalf("exclusive holder did not reach provider: %v", holderCtx.Err())
	}

	waiterCtx, cancelWaiters := context.WithCancel(holderCtx)
	defer cancelWaiters()
	const waiterCount = 12
	waiterDone := make(chan error, waiterCount)
	start := make(chan struct{})
	for i := 0; i < waiterCount; i++ {
		i := i
		go func() {
			<-start
			if i%2 == 0 {
				_, err := registry.ReembedCurrentSpace(waiterCtx, installationID, 10)
				waiterDone <- err
				return
			}
			err := capturedA.(*PGIndexer).ImportDocs(waiterCtx, []Doc{
				lifecycleDoc(fmt.Sprintf("small-pool-writer-%d", i), "writer waiting behind repair"),
			})
			waiterDone <- err
		}()
	}
	close(start)
	// Give both exclusive and shared waiters several chances to observe the
	// busy lock before the holder asks the pool for its UPDATE connection.
	time.Sleep(3 * reembedLockPollInterval)
	releaseProvider()

	select {
	case result := <-holderDone:
		if result.err != nil {
			t.Fatalf("exclusive holder failed with lock waiters present: %v (repaired=%d)", result.err, result.repaired)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exclusive holder was starved of its work connection by advisory-lock waiters")
	}

	cancelWaiters()
	for i := 0; i < waiterCount; i++ {
		select {
		case <-waiterDone:
		case <-time.After(2 * time.Second):
			t.Fatal("advisory-lock waiter did not exit after cancellation")
		}
	}
}

func TestRegistryDistinctTenantRepairsLeavePoolHeadroom(t *testing.T) {
	pool, firstInstallationID := pgTestPoolWithMaxConns(t, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	installationIDs := []int64{firstInstallationID}
	for i := 1; i < 5; i++ {
		var installationID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO installations (installation_id, org_login)
			VALUES ($1, $2)
			ON CONFLICT (installation_id) DO UPDATE SET org_login = EXCLUDED.org_login
			RETURNING id`, 910_060+i, fmt.Sprintf("repair-headroom-%d", i)).Scan(&installationID); err != nil {
			t.Fatalf("create installation %d: %v", i, err)
		}
		installationIDs = append(installationIDs, installationID)
	}
	for i, installationID := range installationIDs {
		model := fmt.Sprintf("headroom-space-%d", i)
		if _, err := pool.Exec(ctx, `
			UPDATE installations
			SET default_settings = jsonb_set(COALESCE(default_settings, '{}'::jsonb),
				'{test_embedding_model}', to_jsonb($2::text), true)
			WHERE id = $1`, installationID, model); err != nil {
			t.Fatalf("configure installation %d: %v", installationID, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO memories (installation_id, container_tag, custom_id, type, content, metadata)
			VALUES ($1, 'repo:api', $2, 'pattern', $3, '{}'::jsonb)
			ON CONFLICT (installation_id, custom_id) DO UPDATE
			SET content = EXCLUDED.content, embedding = NULL, embedding_model = NULL, embedding_space = NULL,
				deleted_at = NULL, invalidated_at = NULL`,
			installationID, fmt.Sprintf("repair-headroom-%d", i), fmt.Sprintf("tenant repair %d", i)); err != nil {
			t.Fatalf("seed installation %d: %v", installationID, err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCleanup()
		if _, err := pool.Exec(cleanupCtx, `DELETE FROM memories WHERE installation_id = ANY($1)`, installationIDs); err != nil {
			t.Errorf("cleanup repair memories: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, `
			UPDATE installations
			SET default_settings = COALESCE(default_settings, '{}'::jsonb) - 'test_embedding_model'
			WHERE id = ANY($1)`, installationIDs); err != nil {
			t.Errorf("cleanup repair installations: %v", err)
		}
	})

	providerEntered := make(chan string, len(installationIDs))
	releaseProviders := make(chan struct{})
	var releaseOnce sync.Once
	releaseProviderCalls := func() { releaseOnce.Do(func() { close(releaseProviders) }) }
	defer releaseProviderCalls()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		providerEntered <- req.Model
		select {
		case <-releaseProviders:
		case <-r.Context().Done():
			return
		}
		data := make([]map[string]any, len(req.Input))
		for i := range req.Input {
			vec := make([]float32, StorageDimensions)
			vec[0] = 1
			data[i] = map[string]any{"index": i, "embedding": vec}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()

	embedders := NewEmbedderRegistry(
		&pgDesiredSpaceResolver{pool: pool, baseURL: server.URL},
		PlatformEmbeddings{Dimensions: StorageDimensions},
		discardLogger(),
	)
	registry := NewRegistry(discardLogger()).WithPostgresBackend(pool, embedders)

	// Model the durable EventBus LISTEN session, which permanently occupies one
	// production pool connection. Each repair below has a distinct tenant lock.
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("reserve durable listener connection: %v", err)
	}
	defer listener.Release()

	results := make(chan reembedResult, len(installationIDs))
	for _, installationID := range installationIDs {
		installationID := installationID
		go func() {
			n, err := registry.ReembedCurrentSpace(ctx, installationID, 10)
			results <- reembedResult{repaired: n, err: err}
		}()
	}

	seenModels := make(map[string]bool, len(installationIDs))
	// Six connections admit two repairs (two connections each), while one
	// listener and one ordinary query slot remain available.
	for len(seenModels) < 2 {
		select {
		case model := <-providerEntered:
			seenModels[model] = true
		case <-ctx.Done():
			t.Fatalf("only %d repairs reached the provider barrier: %v", len(seenModels), ctx.Err())
		}
	}

	// A queued caller must be able to abandon the process-local permit wait
	// without borrowing a pool connection or leaking capacity.
	cancelledCtx, cancelQueued := context.WithCancel(ctx)
	cancelledDone := make(chan error, 1)
	queuedStarted := make(chan struct{})
	go func() {
		close(queuedStarted)
		_, err := registry.ReembedCurrentSpace(cancelledCtx, installationIDs[0], 10)
		cancelledDone <- err
	}()
	<-queuedStarted
	cancelQueued()
	select {
	case err := <-cancelledDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled repair permit wait error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled repair permit waiter did not exit")
	}
	// Give the old implementation time to park every distinct-tenant winner at
	// the barrier with its advisory-lock connection. A capped Registry leaves
	// one work connection per admitted repair and one ordinary query slot in
	// addition to the durable listener.
	timer := time.NewTimer(3 * reembedLockPollInterval)
collectEarly:
	for {
		select {
		case model := <-providerEntered:
			seenModels[model] = true
		case <-timer.C:
			break collectEarly
		}
	}
	if len(seenModels) != 2 {
		t.Errorf("repairs admitted at provider barrier = %d, want capacity-safe 2", len(seenModels))
	}

	queryCtx, cancelQuery := context.WithTimeout(ctx, 300*time.Millisecond)
	var one int
	queryErr := pool.QueryRow(queryCtx, `SELECT 1`).Scan(&one)
	cancelQuery()
	if queryErr != nil {
		t.Errorf("normal pool query starved behind distinct-tenant repairs: %v", queryErr)
	} else if one != 1 {
		t.Errorf("normal pool query = %d, want 1", one)
	}

	releaseProviderCalls()
	for range installationIDs {
		select {
		case result := <-results:
			if result.err != nil {
				t.Errorf("repair failed: %v (repaired=%d)", result.err, result.repaired)
			} else if result.repaired != 1 {
				t.Errorf("repaired = %d, want 1", result.repaired)
			}
		case <-ctx.Done():
			t.Fatalf("distinct-tenant repairs did not all progress: %v", ctx.Err())
		}
	}
	for len(seenModels) < len(installationIDs) {
		select {
		case model := <-providerEntered:
			seenModels[model] = true
		default:
			t.Fatalf("provider saw models %v, want all %d tenant repairs", seenModels, len(installationIDs))
		}
	}
}
