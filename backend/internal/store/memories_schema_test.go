package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// memoriesTestGHInstall is a sentinel GitHub installation number far outside
// real IDs so schema tests never collide with other PG-backed tests sharing
// the CI database. The internal installations.id rows are created per test.
const memoriesTestGHInstall = int64(910_057)

// createTestInstallation upserts an installations row and returns its internal
// id — memories.installation_id has an FK to installations(id), so tests must
// use real tenants (which is itself part of the contract under test).
func createTestInstallation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ghID int64) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ($1, 'memories-schema-test')
		ON CONFLICT (installation_id) DO UPDATE SET org_login = EXCLUDED.org_login
		RETURNING id`, ghID).Scan(&id); err != nil {
		t.Fatalf("create test installation: %v", err)
	}
	return id
}

// TestMemoriesSchema exercises migration 057's contracts end-to-end against
// the CI database: upsert-REPLACE semantics, tenant-scoped customId
// uniqueness, the generated FTS column, jsonb containment filtering, and the
// NULL-embedding fail-open write path.
func TestMemoriesSchema(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	install := createTestInstallation(t, ctx, pool, memoriesTestGHInstall)
	other := createTestInstallation(t, ctx, pool, memoriesTestGHInstall+1)

	cleanup := func() {
		_, err := pool.Exec(ctx, "DELETE FROM memories WHERE installation_id = ANY($1::bigint[])", []int64{install, other})
		if err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	}
	cleanup()
	defer cleanup()

	const upsert = `
		INSERT INTO memories (installation_id, container_tag, custom_id, type, content, metadata, embedding, embedding_model)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (installation_id, custom_id) DO UPDATE
		SET content = EXCLUDED.content, metadata = EXCLUDED.metadata,
		    embedding = EXCLUDED.embedding, embedding_model = EXCLUDED.embedding_model,
		    container_tag = EXCLUDED.container_tag, updated_at = now(), deleted_at = NULL`

	vec := make([]float32, 1536)
	vec[0] = 1 // unit vector along the first axis
	vecLit := float32SliceToVectorLiteral(vec)

	// Insert with an embedding.
	if _, err := pool.Exec(ctx, upsert,
		install, "repo-a", "repo-a--dismissal--h1", "feedback",
		"nil pointer dereference in payment handler",
		map[string]string{"action": "dismissed", "category": "bug_risk"},
		vecLit, "text-embedding-3-small",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Fail-open path: a second doc with NULL embedding must land.
	if _, err := pool.Exec(ctx, upsert,
		install, "repo-a", "repo-a--pattern--h2", "pattern",
		"always use guard clauses instead of nested conditionals",
		map[string]string{"source": "confirmed", "confidence": "0.90"},
		nil, nil,
	); err != nil {
		t.Fatalf("insert with NULL embedding: %v", err)
	}

	// Soft-delete then upsert the same customId: REPLACE semantics must win —
	// content replaced wholesale (no Supermemory-style merge) and deleted_at
	// reset to NULL.
	if _, err := pool.Exec(ctx,
		"UPDATE memories SET deleted_at = now() WHERE installation_id = $1 AND custom_id = $2",
		install, "repo-a--dismissal--h1"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := pool.Exec(ctx, upsert,
		install, "repo-a", "repo-a--dismissal--h1", "feedback",
		"REPLACED body after re-dismissal",
		map[string]string{"action": "dismissed", "category": "bug_risk", "reason": "known false positive"},
		vecLit, "text-embedding-3-small",
	); err != nil {
		t.Fatalf("upsert replace: %v", err)
	}
	var content string
	var deletedAt *time.Time
	if err := pool.QueryRow(ctx,
		"SELECT content, deleted_at FROM memories WHERE installation_id = $1 AND custom_id = $2",
		install, "repo-a--dismissal--h1").Scan(&content, &deletedAt); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if content != "REPLACED body after re-dismissal" {
		t.Fatalf("upsert did not REPLACE content: got %q", content)
	}
	if deletedAt != nil {
		t.Fatal("upsert did not resurrect the soft-deleted row")
	}

	// Same customId under a DIFFERENT installation must be a distinct row.
	if _, err := pool.Exec(ctx, upsert,
		other, "repo-a", "repo-a--dismissal--h1", "feedback",
		"other tenant's doc", map[string]string{"action": "dismissed"}, nil, nil,
	); err != nil {
		t.Fatalf("cross-tenant insert: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM memories WHERE custom_id = $1 AND installation_id IN ($2, $3)",
		"repo-a--dismissal--h1", install, other).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("tenant scoping: want 2 rows across installations, got %d", n)
	}

	// The FK guard: a bogus installation id must fail loudly, never file
	// silently under a phantom tenant.
	if _, err := pool.Exec(ctx, upsert,
		int64(999_999_999), "repo-a", "repo-a--bogus--h9", "pattern",
		"orphan doc", map[string]string{}, nil, nil,
	); err == nil {
		t.Fatal("insert with nonexistent installation_id succeeded; FK missing")
	}

	// Generated tsvector column indexes the content for FTS.
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM memories
		WHERE installation_id = $1
		  AND content_tsv @@ plainto_tsquery('english', 'guard clauses')`,
		install).Scan(&n); err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if n != 1 {
		t.Fatalf("fts: want 1 hit for 'guard clauses', got %d", n)
	}

	// jsonb containment filter (the equality-filter compile target).
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM memories
		WHERE installation_id = $1 AND metadata @> '{"action":"dismissed"}'`,
		install).Scan(&n); err != nil {
		t.Fatalf("metadata containment: %v", err)
	}
	if n != 1 {
		t.Fatalf("metadata filter: want 1, got %d", n)
	}

	// Cosine similarity on the stored embedding follows the score contract.
	var sim float64
	if err := pool.QueryRow(ctx, `
		SELECT 1 - (embedding <=> $2::vector)
		FROM memories WHERE installation_id = $1 AND embedding IS NOT NULL`,
		install, vecLit).Scan(&sim); err != nil {
		t.Fatalf("cosine: %v", err)
	}
	if sim < 0.9999 {
		t.Fatalf("self-similarity: want ~1.0, got %v", sim)
	}

	// The partial HNSW index from 057 exists.
	var idxCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM pg_indexes WHERE tablename = 'memories' AND indexname = 'memories_embedding_hnsw'").
		Scan(&idxCount); err != nil {
		t.Fatalf("pg_indexes: %v", err)
	}
	if idxCount != 1 {
		t.Fatal("memories_embedding_hnsw index missing")
	}
}

// float32SliceToVectorLiteral renders a pgvector text literal ('[1,0,...]').
// The pgIndexer will use a typed codec; for schema tests the literal is enough.
func float32SliceToVectorLiteral(v []float32) string {
	b := make([]byte, 0, len(v)*2+2)
	b = append(b, '[')
	for i, f := range v {
		if i > 0 {
			b = append(b, ',')
		}
		if f == float32(int64(f)) {
			b = appendInt(b, int64(f))
		} else {
			// Not needed for current tests; keep the fast integer path honest.
			panic("float32SliceToVectorLiteral: non-integer components unsupported in test helper")
		}
	}
	return string(append(b, ']'))
}

func appendInt(b []byte, n int64) []byte {
	if n == 0 {
		return append(b, '0')
	}
	if n < 0 {
		b = append(b, '-')
		n = -n
	}
	var tmp [20]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	return append(b, tmp[i:]...)
}
