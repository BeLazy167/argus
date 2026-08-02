package store

import (
	"context"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store/migrations"
)

// embeddedChainHead returns the highest NNN_ prefix among the embedded
// migration files, so the harness assertion tracks the chain automatically.
func embeddedChainHead(t *testing.T) uint64 {
	t.Helper()
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var head uint64
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(prefix, 10, 64)
		if err != nil {
			continue
		}
		if n > head {
			head = n
		}
	}
	if head == 0 {
		t.Fatal("no numbered migrations found in embedded FS")
	}
	return head
}

// TestCIPostgresHarness proves the CI database harness end-to-end: a reachable
// Postgres with the FULL migration chain applied (the Migrate step runs before
// tests) and the pgvector extension installable with working distance math.
// PG-backed tests introduced by the memory-in-Postgres program build on this
// gate.
//
// Gated on TEST_DATABASE_URL — deliberately NOT the app's DATABASE_URL — so a
// developer with live credentials exported can never have `go test ./...`
// touch that database.
func TestCIPostgresHarness(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1: got %d, err %v", one, err)
	}

	// The FULL migration chain ran: version matches the embedded head, clean.
	var version uint64
	var dirty bool
	if err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if dirty {
		t.Fatal("migration chain is dirty")
	}
	if head := embeddedChainHead(t); version != head {
		t.Fatalf("migration version: want embedded chain head %d, got %d", head, version)
	}

	// pgvector is available in the image (migration 057 will own the real
	// CREATE EXTENSION; IF NOT EXISTS keeps this test idempotent with it).
	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		t.Fatalf("CREATE EXTENSION vector: %v", err)
	}

	// Cosine distance math works: orthogonal unit vectors have similarity 0.
	var sim float64
	if err := pool.QueryRow(ctx, "SELECT 1 - ('[1,0,0]'::vector <=> '[0,1,0]'::vector)").Scan(&sim); err != nil {
		t.Fatalf("vector cosine query: %v", err)
	}
	if math.Abs(sim) > 1e-9 {
		t.Fatalf("orthogonal cosine similarity: want 0, got %v", sim)
	}
}
