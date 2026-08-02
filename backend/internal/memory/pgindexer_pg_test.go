package memory

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
)

// pgTestDims matches memories.embedding vector(1024).
const pgTestDims = 1024

// stubEmbedder returns deterministic 1024-dim vectors (first component =
// content length) or a fixed error when failWith is set.
type stubEmbedder struct {
	failWith error
	dims     int // 0 = pgTestDims
	shortBy  int // return len(inputs)-shortBy vectors (contract violation)
	calls    int
}

func (s *stubEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	s.calls++
	if s.failWith != nil {
		return nil, s.failWith
	}
	dims := s.dims
	if dims == 0 {
		dims = pgTestDims
	}
	n := len(inputs) - s.shortBy
	if n < 0 {
		n = 0
	}
	out := make([][]float32, n)
	for i := range out {
		v := make([]float32, dims)
		v[0] = float32(len(inputs[i]))
		out[i] = v
	}
	return out, nil
}

func (s *stubEmbedder) Model() string { return "stub-1024" }

func pgTestPool(t *testing.T) (*pgxpool.Pool, int64) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvector.RegisterTypes(ctx, conn)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	var install int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES (910_060, 'pgindexer-test')
		ON CONFLICT (installation_id) DO UPDATE SET org_login = EXCLUDED.org_login
		RETURNING id`).Scan(&install); err != nil {
		t.Fatalf("test installation: %v", err)
	}
	cleanup := func() {
		_, err := pool.Exec(context.Background(), "DELETE FROM memories WHERE installation_id = $1", install)
		if err != nil {
			t.Errorf("cleanup: %v", err)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	return pool, install
}

type memRow struct {
	containerTag string
	docType      string
	content      string
	metadata     map[string]string
	hasEmbedding bool
	model        *string
	deletedAt    *time.Time
}

func readRow(t *testing.T, pool *pgxpool.Pool, install int64, customID string) memRow {
	t.Helper()
	var r memRow
	err := pool.QueryRow(context.Background(), `
		SELECT container_tag, type, content, metadata, embedding IS NOT NULL, embedding_model, deleted_at
		FROM memories WHERE installation_id = $1 AND custom_id = $2`,
		install, customID).Scan(&r.containerTag, &r.docType, &r.content, &r.metadata, &r.hasEmbedding, &r.model, &r.deletedAt)
	if err != nil {
		t.Fatalf("read row %s: %v", customID, err)
	}
	return r
}

func TestPGIndexerWriters(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	emb := &stubEmbedder{}
	idx := NewPGIndexer(pool, emb, install, pgTestDims, slog.New(slog.DiscardHandler))

	t.Run("review batch with duplicate customIds", func(t *testing.T) {
		// Two identical findings (same file/category/body) — the exact input
		// that raised a cardinality violation before the LWW dedupe; third doc
		// distinct. Also proves ONE embed call for the whole batch.
		before := emb.calls
		comments := []ReviewMemory{
			{ReviewID: "r1", PRNumber: 7, FilePath: "a.go", Body: "nil deref in handler", Severity: "warning", Category: "bug_risk"},
			{ReviewID: "r2", PRNumber: 8, FilePath: "a.go", Body: "nil deref in handler", Severity: "warning", Category: "bug_risk"},
			{ReviewID: "r3", PRNumber: 8, FilePath: "b.go", Body: "unchecked error return", Severity: "suggestion", Category: "bug_risk"},
		}
		if err := idx.IndexReviewCommentsBatch(ctx, "acme", "api", comments); err != nil {
			t.Fatalf("batch: %v", err)
		}
		if emb.calls != before+1 {
			t.Fatalf("embed calls = %d, want 1 per batch", emb.calls-before)
		}
		dup := FindingFingerprint("acme", "api", "a.go", "bug_risk", "nil deref in handler")
		r := readRow(t, pool, install, dup)
		if r.metadata["review_id"] != "r2" {
			t.Fatalf("LWW dedupe: want last write (r2), got %q", r.metadata["review_id"])
		}
		if !r.hasEmbedding || r.model == nil || *r.model != "stub-1024" {
			t.Fatalf("embedding not stamped: has=%v model=%v", r.hasEmbedding, r.model)
		}
		if r.docType != "review" || r.containerTag != RepoTagNew("api") {
			t.Fatalf("row shape: type=%q tag=%q", r.docType, r.containerTag)
		}
	})

	t.Run("rule to shared", func(t *testing.T) {
		if err := idx.IndexRule(ctx, "acme", RuleMemory{RuleID: 42, Category: "style", Priority: 2, Content: "prefer guard clauses"}); err != nil {
			t.Fatalf("rule: %v", err)
		}
		r := readRow(t, pool, install, RuleCustomID(42))
		if r.containerTag != SharedTag || r.docType != "rule" || r.metadata["rule_id"] != "42" {
			t.Fatalf("rule row: %+v", r)
		}
	})

	t.Run("pattern returns customId as ID and re-types synthesis", func(t *testing.T) {
		resp, err := idx.IndexPattern(ctx, "api", PatternMemory{Content: "file summary text", Source: "synthesis", FilePath: "a.go"})
		if err != nil {
			t.Fatalf("pattern: %v", err)
		}
		if resp == nil || resp.ID == "" {
			t.Fatal("want customId as AddResponse.ID")
		}
		r := readRow(t, pool, install, resp.ID)
		if r.docType != "synthesis" {
			t.Fatalf("source re-typing: got type %q", r.docType)
		}
		if r.metadata["custom_id"] != resp.ID {
			t.Fatal("custom_id mirror missing from metadata")
		}
	})

	t.Run("shared pattern pins confidence", func(t *testing.T) {
		resp, err := idx.IndexSharedPattern(ctx, PatternMemory{Content: "org-wide error wrapping", Source: "org_learned"})
		if err != nil {
			t.Fatalf("shared pattern: %v", err)
		}
		r := readRow(t, pool, install, resp.ID)
		if r.containerTag != SharedTag || r.metadata["confidence"] != "1.00" {
			t.Fatalf("shared row: tag=%q conf=%q", r.containerTag, r.metadata["confidence"])
		}
	})

	t.Run("dismissal feedback keying and provenance", func(t *testing.T) {
		fb := FeedbackMemory{
			FilePath: "a.go", Category: "bug_risk", OriginalBody: "false positive claim",
			Action: "dismissed", DeveloperReply: "this is guarded upstream", PRNumber: 7,
			ChangeKind: "production", Reason: "guarded by caller", Repo: "api",
		}
		if err := idx.IndexFeedbackSignal(ctx, "acme", "api", fb); err != nil {
			t.Fatalf("feedback: %v", err)
		}
		id := dismissalCustomID("api", "bug_risk", "false positive claim")
		r := readRow(t, pool, install, id)
		if r.metadata["action"] != "dismissed" || r.metadata["polarity"] != "negative" {
			t.Fatalf("dismissal metadata: %+v", r.metadata)
		}
		if r.metadata["change_kind"] != "production" || r.metadata["reason"] != "guarded by caller" {
			t.Fatalf("dismissal extras: %+v", r.metadata)
		}
		if r.content != "false positive claim\n\nDeveloper explanation: this is guarded upstream" {
			t.Fatalf("feedback content shape: %q", r.content)
		}
	})

	t.Run("unsupported feedback action errors", func(t *testing.T) {
		err := idx.IndexFeedbackSignal(ctx, "acme", "api", FeedbackMemory{Action: "applauded", OriginalBody: "x"})
		if err == nil {
			t.Fatal("want error for unsupported action")
		}
	})

	t.Run("scenario", func(t *testing.T) {
		if err := idx.IndexScenario(ctx, "acme", "api", 9, "race on shutdown", "high", []string{"srv.go", "worker.go"}); err != nil {
			t.Fatalf("scenario: %v", err)
		}
		r := readRow(t, pool, install, ScenarioCustomID("api", 9))
		if r.metadata["scenario_id"] != "9" || r.content != "race on shutdown\n\nRelated files: srv.go, worker.go" {
			t.Fatalf("scenario row: %+v", r)
		}
	})

	t.Run("delete soft-deletes and upsert resurrects", func(t *testing.T) {
		id := ScenarioCustomID("api", 9)
		if err := idx.DeleteDocument(ctx, id); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if r := readRow(t, pool, install, id); r.deletedAt == nil {
			t.Fatal("soft delete did not stamp deleted_at")
		}
		if err := idx.IndexScenario(ctx, "acme", "api", 9, "race on shutdown", "high", nil); err != nil {
			t.Fatalf("re-index: %v", err)
		}
		if r := readRow(t, pool, install, id); r.deletedAt != nil {
			t.Fatal("upsert did not resurrect the soft-deleted row")
		}
	})
}

func TestPGIndexerEmbedFailOpen(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()

	t.Run("embedder error lands NULL embedding", func(t *testing.T) {
		idx := NewPGIndexer(pool, &stubEmbedder{failWith: fmt.Errorf("provider down")}, install, pgTestDims, slog.New(slog.DiscardHandler))
		if err := idx.IndexRule(ctx, "acme", RuleMemory{RuleID: 77, Content: "still lands"}); err != nil {
			t.Fatalf("write must not fail on embed error: %v", err)
		}
		r := readRow(t, pool, install, RuleCustomID(77))
		if r.hasEmbedding || r.model != nil {
			t.Fatalf("want NULL embedding on embed failure, got has=%v model=%v", r.hasEmbedding, r.model)
		}
	})

	t.Run("nil embedder lands NULL embedding", func(t *testing.T) {
		idx := NewPGIndexer(pool, nil, install, pgTestDims, slog.New(slog.DiscardHandler))
		if err := idx.IndexRule(ctx, "acme", RuleMemory{RuleID: 78, Content: "embeddings off"}); err != nil {
			t.Fatalf("write with nil embedder: %v", err)
		}
		if r := readRow(t, pool, install, RuleCustomID(78)); r.hasEmbedding {
			t.Fatal("nil embedder must land NULL embedding")
		}
	})
}

// TestPGIndexerContractViolationsFailOpen: wrong dims or wrong count from the
// embedder must degrade to NULL embeddings (fail-open), never a hard write
// failure (pgvector would reject the INSERT) and never a panic.
func TestPGIndexerContractViolationsFailOpen(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()

	t.Run("wrong dims lands NULL", func(t *testing.T) {
		idx := NewPGIndexer(pool, &stubEmbedder{dims: 768}, install, pgTestDims, slog.New(slog.DiscardHandler))
		if err := idx.IndexRule(ctx, "acme", RuleMemory{RuleID: 81, Content: "wrong dims endpoint"}); err != nil {
			t.Fatalf("wrong-dims write must fail open, got: %v", err)
		}
		if r := readRow(t, pool, install, RuleCustomID(81)); r.hasEmbedding {
			t.Fatal("wrong-dims vector must not be persisted")
		}
	})

	t.Run("short vector count lands NULL, no panic", func(t *testing.T) {
		idx := NewPGIndexer(pool, &stubEmbedder{shortBy: 1}, install, pgTestDims, slog.New(slog.DiscardHandler))
		comments := []ReviewMemory{
			{ReviewID: "s1", PRNumber: 1, FilePath: "x.go", Body: "finding one", Severity: "warning", Category: "bug_risk"},
			{ReviewID: "s2", PRNumber: 1, FilePath: "y.go", Body: "finding two", Severity: "warning", Category: "bug_risk"},
		}
		if err := idx.IndexReviewCommentsBatch(ctx, "acme", "api", comments); err != nil {
			t.Fatalf("short-count write must fail open, got: %v", err)
		}
		r := readRow(t, pool, install, FindingFingerprint("acme", "api", "x.go", "bug_risk", "finding one"))
		if r.hasEmbedding {
			t.Fatal("short-count batch must land unembedded")
		}
	})
}

// TestPGIndexerInvalidationPreserved: a re-upsert of the same customId must
// NOT clear invalidated_at/superseded_by — invalidation is a policy judgment
// a mechanical re-write cannot overturn (gate finding, 2/3 confirmed).
func TestPGIndexerInvalidationPreserved(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, &stubEmbedder{}, install, pgTestDims, slog.New(slog.DiscardHandler))

	fb := FeedbackMemory{
		FilePath: "a.go", Category: "security", OriginalBody: "dismissed vuln claim",
		Action: "dismissed", PRNumber: 3, Repo: "api",
	}
	if err := idx.IndexFeedbackSignal(ctx, "acme", "api", fb); err != nil {
		t.Fatalf("first dismissal: %v", err)
	}
	id := dismissalCustomID("api", "security", "dismissed vuln claim")
	if _, err := pool.Exec(ctx,
		"UPDATE memories SET invalidated_at = now() WHERE installation_id = $1 AND custom_id = $2",
		install, id); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	// Identical re-dismissal upserts the same customId.
	if err := idx.IndexFeedbackSignal(ctx, "acme", "api", fb); err != nil {
		t.Fatalf("re-dismissal: %v", err)
	}
	var invalidated *time.Time
	if err := pool.QueryRow(ctx,
		"SELECT invalidated_at FROM memories WHERE installation_id = $1 AND custom_id = $2",
		install, id).Scan(&invalidated); err != nil {
		t.Fatalf("read invalidated_at: %v", err)
	}
	if invalidated == nil {
		t.Fatal("re-upsert cleared invalidated_at; invalidation must survive mechanical re-writes")
	}
}

// TestPGIndexerTypeFollowsRewrite: re-upserting a customId under a different
// doc shape must move the type COLUMN with the metadata — search filters on
// the column, and a stale column silently misclassifies the row (gate
// finding: SET list omitted type).
func TestPGIndexerTypeFollowsRewrite(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, &stubEmbedder{}, install, pgTestDims, slog.New(slog.DiscardHandler))

	// Source "synthesis" types the doc TypeSynthesis; "pattern" types it
	// TypePattern (source-based re-typing in PatternMemory.metadata).
	first := PatternMemory{CustomID: "typedrift--probe", Source: "synthesis", FilePath: "a.go", Content: "v1"}
	if _, err := idx.IndexPattern(ctx, "api", first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := PatternMemory{CustomID: "typedrift--probe", Source: "pattern", FilePath: "a.go", Content: "v2"}
	if _, err := idx.IndexPattern(ctx, "api", second); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	var colType, metaType string
	if err := pool.QueryRow(ctx,
		"SELECT type, metadata->>'type' FROM memories WHERE installation_id = $1 AND custom_id = $2",
		install, "typedrift--probe").Scan(&colType, &metaType); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if colType != metaType {
		t.Fatalf("type column %q contradicts metadata type %q after rewrite", colType, metaType)
	}
	if colType != string(TypePattern) {
		t.Fatalf("type = %q, want %q (rewrite must win)", colType, TypePattern)
	}
}

// TestPGIndexerNULContentStripped: Postgres TEXT rejects NUL (22021) where
// Supermemory accepted it, and one poisoned doc aborts the whole batch
// transaction — the writer strips NUL instead of losing the batch.
func TestPGIndexerNULContentStripped(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, &stubEmbedder{}, install, pgTestDims, slog.New(slog.DiscardHandler))

	fb := FeedbackMemory{
		FilePath: "a.go", Category: "bug_risk", OriginalBody: "claim with NUL \x00 byte",
		DeveloperReply: "also\x00here", Action: "dismissed", PRNumber: 4, Repo: "api",
	}
	if err := idx.IndexFeedbackSignal(ctx, "acme", "api", fb); err != nil {
		t.Fatalf("NUL content must not fail the write: %v", err)
	}
	var content string
	if err := pool.QueryRow(ctx,
		"SELECT content FROM memories WHERE installation_id = $1 AND type = $2 AND content LIKE '%claim with NUL%'",
		install, string(TypeFeedback)).Scan(&content); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if strings.Contains(content, "\x00") {
		t.Fatal("NUL byte survived into stored content")
	}
}

// TestPGIndexerEmptyCustomIDSkipped: a pathless review comment derives an
// empty fingerprint; it must be skipped (never LWW-collapse unrelated docs
// onto a shared "" key), while valid siblings land.
func TestPGIndexerEmptyCustomIDSkipped(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, &stubEmbedder{}, install, pgTestDims, slog.New(slog.DiscardHandler))

	comments := []ReviewMemory{
		{ReviewID: "p1", PRNumber: 2, FilePath: "", Body: "pathless PR-level note", Severity: "suggestion", Category: "style"},
		{ReviewID: "p2", PRNumber: 2, FilePath: "z.go", Body: "real finding", Severity: "warning", Category: "bug_risk"},
	}
	if err := idx.IndexReviewCommentsBatch(ctx, "acme", "api", comments); err != nil {
		t.Fatalf("batch with pathless comment: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM memories WHERE installation_id = $1 AND custom_id = ''", install).Scan(&n); err != nil {
		t.Fatalf("count empty ids: %v", err)
	}
	if n != 0 {
		t.Fatalf("empty-customId rows persisted: %d", n)
	}
	readRow(t, pool, install, FindingFingerprint("acme", "api", "z.go", "bug_risk", "real finding"))
}
