package store

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func graphBudgetTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func TestTryReserveGraphIndexWindowIsFleetGlobalAndPersistent(t *testing.T) {
	pool, ctx := graphBudgetTestPool(t)
	st := NewWithDB(pool)
	if _, err := pool.Exec(ctx, `UPDATE graph_index_budget SET last_window_started_at = NULL WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE graph_index_budget SET last_window_started_at = NULL WHERE singleton`)
	})

	const contenders = 8
	start := make(chan struct{})
	var admitted atomic.Int32
	var wg sync.WaitGroup
	wg.Add(contenders)
	for range contenders {
		go func() {
			defer wg.Done()
			<-start
			ok, err := st.TryReserveGraphIndexWindow(ctx, 5*time.Minute)
			if err != nil {
				t.Errorf("reserve: %v", err)
				return
			}
			if ok {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := admitted.Load(); got != 1 {
		t.Fatalf("concurrent fleet admissions = %d, want 1", got)
	}

	// A fresh Store represents a restarted process. The persisted timestamp must
	// still reject it until the DB-owned minimum spacing has elapsed.
	restarted := NewWithDB(pool)
	if ok, err := restarted.TryReserveGraphIndexWindow(ctx, 5*time.Minute); err != nil || ok {
		t.Fatalf("restart admission = %v, err=%v; want denied", ok, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE graph_index_budget
		SET last_window_started_at = NOW() - interval '5 minutes' WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	if ok, err := restarted.TryReserveGraphIndexWindow(ctx, 5*time.Minute); err != nil || !ok {
		t.Fatalf("exact-spacing admission = %v, err=%v; want admitted", ok, err)
	}
}
