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
	return pgTestPoolWithMaxConns(t, 0)
}

func pgTestPoolWithMaxConns(t *testing.T, maxConns int32) (*pgxpool.Pool, int64) {
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
	if maxConns > 0 {
		cfg.MaxConns = maxConns
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
	containerTag   string
	docType        string
	content        string
	metadata       map[string]string
	hasEmbedding   bool
	model          *string
	embeddingSpace *string
	deletedAt      *time.Time
}

func readRow(t *testing.T, pool *pgxpool.Pool, install int64, customID string) memRow {
	t.Helper()
	var r memRow
	err := pool.QueryRow(context.Background(), `
		SELECT container_tag, type, content, metadata, embedding IS NOT NULL, embedding_model, embedding_space, deleted_at
		FROM memories WHERE installation_id = $1 AND custom_id = $2`,
		install, customID).Scan(&r.containerTag, &r.docType, &r.content, &r.metadata, &r.hasEmbedding, &r.model, &r.embeddingSpace, &r.deletedAt)
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
			t.Fatal("want customId as IndexResult.ID")
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
// and one poisoned doc aborts the whole batch
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

// ReembedMissing is the repair for the write path's fail-open behaviour: a row
// written while the embedder was broken carries a NULL vector and is reachable
// through the full-text leg only.
func TestPGIndexerReembedMissing(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()

	// Write two rows with NO embedder — the fail-open path.
	blind := NewPGIndexer(pool, nil, install, StorageDimensions, discardLogger())
	if err := blind.ImportDocs(ctx, []Doc{
		{ContainerTag: "repo", CustomID: "reembed-a", Type: "pattern", Content: "alpha", Metadata: map[string]string{"type": "pattern"}},
		{ContainerTag: "repo", CustomID: "reembed-b", Type: "pattern", Content: "beta", Metadata: map[string]string{"type": "pattern"}},
	}); err != nil {
		t.Fatalf("seed unembedded rows: %v", err)
	}
	for _, id := range []string{"reembed-a", "reembed-b"} {
		if readRow(t, pool, install, id).hasEmbedding {
			t.Fatalf("%s should have landed unembedded", id)
		}
	}

	idx := NewPGIndexer(pool, &stubEmbedder{dims: StorageDimensions}, install, StorageDimensions, discardLogger())
	repaired, err := idx.ReembedMissing(ctx, 10)
	if err != nil {
		t.Fatalf("ReembedMissing: %v", err)
	}
	if repaired != 2 {
		t.Errorf("repaired = %d, want 2", repaired)
	}
	for _, id := range []string{"reembed-a", "reembed-b"} {
		row := readRow(t, pool, install, id)
		if !row.hasEmbedding {
			t.Errorf("%s still has no embedding", id)
		}
		if row.model == nil || *row.model != "stub-1024" {
			t.Errorf("%s embedding_model = %v, want stub-1024", id, row.model)
		}
		// Content must be untouched: a re-embed that rewrote content would
		// resurrect text the row no longer holds.
		if want := map[string]string{"reembed-a": "alpha", "reembed-b": "beta"}[id]; row.content != want {
			t.Errorf("%s content = %q, want %q (re-embed must not rewrite content)", id, row.content, want)
		}
	}

	// A non-NULL vector in a foreign space is just as unusable as a missing
	// vector and must be repaired after endpoint/model rotation.
	if _, err := pool.Exec(ctx, `UPDATE memories SET embedding_space = 'retired-space'
		WHERE installation_id = $1 AND custom_id = 'reembed-a'`, install); err != nil {
		t.Fatalf("rotate space: %v", err)
	}
	again, err := idx.ReembedMissing(ctx, 10)
	if err != nil {
		t.Fatalf("mismatched-space pass: %v", err)
	}
	if again != 1 {
		t.Errorf("mismatched-space pass repaired = %d, want 1", again)
	}
	if got := readRow(t, pool, install, "reembed-a").embeddingSpace; got == nil || *got != idx.embeddingSpaceID() {
		t.Errorf("embedding_space = %v, want current %q", got, idx.embeddingSpaceID())
	}

	// A clean corpus is a no-op, not an error.
	clean, err := idx.ReembedMissing(ctx, 10)
	if err != nil {
		t.Fatalf("clean pass: %v", err)
	}
	if clean != 0 {
		t.Errorf("clean pass repaired = %d, want 0", clean)
	}
}

// A row whose content changes after selection must not receive the stale
// vector, and the sweep must NOT report success while it is still unembedded.
func TestPGIndexerReembedSkipsRacedContentAndReportsIt(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()

	blind := NewPGIndexer(pool, nil, install, StorageDimensions, discardLogger())
	if err := blind.ImportDocs(ctx, []Doc{
		{ContainerTag: "repo", CustomID: "raced", Type: "pattern", Content: "original", Metadata: map[string]string{"type": "pattern"}},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Simulate the race: rewrite the content (still unembedded) so the
	// content-guarded UPDATE can never match.
	racer := &racingEmbedder{pool: pool, install: install, dims: StorageDimensions}
	idx := NewPGIndexer(pool, racer, install, StorageDimensions, discardLogger())

	repaired, err := idx.ReembedMissing(ctx, 10)
	if repaired != 0 {
		t.Errorf("repaired = %d, want 0 — the raced row must not take a stale vector", repaired)
	}
	if err == nil {
		t.Fatal("a page that makes zero progress while rows remain unembedded must not report success")
	}
	if row := readRow(t, pool, install, "raced"); row.hasEmbedding {
		t.Error("raced row received an embedding derived from content it no longer has")
	}
}

// racingEmbedder rewrites the row's content during Embed, reproducing an
// upsert that lands between this sweep's SELECT and its UPDATE.
type racingEmbedder struct {
	pool    *pgxpool.Pool
	install int64
	dims    int
	n       int
}

func (r *racingEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	r.n++
	_, err := r.pool.Exec(ctx,
		`UPDATE memories SET content = $3 WHERE installation_id = $1 AND custom_id = $2`,
		r.install, "raced", fmt.Sprintf("rewritten-%d", r.n))
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(inputs))
	for i := range out {
		v := make([]float32, r.dims)
		v[0] = 1
		out[i] = v
	}
	return out, nil
}

func (r *racingEmbedder) Model() string { return "racing-1024" }

func TestPGIndexerReactionFeedbackReversalPreservesOtherSources(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, &stubEmbedder{}, install, pgTestDims, slog.New(slog.DiscardHandler))
	base := FeedbackMemory{
		FilePath: "race.go", Category: "concurrency", OriginalBody: "lock ordering can deadlock",
		PRNumber: 9, Repo: "api",
	}

	trusted := base
	trusted.Action = "dismissed"
	trusted.Source = SourceTrustedReplyFeedback
	trusted.DeveloperReply = "this lock is intentionally one-way"
	if err := idx.IndexFeedbackSignal(ctx, "acme", "api", trusted); err != nil {
		t.Fatal(err)
	}
	trustedID := dismissalCustomIDForSource("api", base.Category, base.OriginalBody, SourceTrustedReplyFeedback)

	// Model a pre-source trusted reply row from before origin-specific IDs. The
	// reaction reconciler may clean legacy reaction rows, but not one whose
	// developer explanation proves it came from the authorized reply path.
	legacyTrusted := base
	legacyTrusted.Action = "dismissed"
	legacyTrusted.DeveloperReply = "legacy trusted explanation"
	if err := idx.IndexFeedbackSignal(ctx, "acme", "api", legacyTrusted); err != nil {
		t.Fatal(err)
	}
	legacyTrustedID := dismissalCustomID("api", base.Category, base.OriginalBody)

	legacyReaction := base
	legacyReaction.OriginalBody = "legacy reaction dismissal"
	legacyReaction.Action = "dismissed"
	if err := idx.IndexFeedbackSignal(ctx, "acme", "api", legacyReaction); err != nil {
		t.Fatal(err)
	}
	legacyReactionID := dismissalCustomID("api", legacyReaction.Category, legacyReaction.OriginalBody)
	legacyReaction.Source = SourceReactionFeedback
	legacyReaction.Action = ""
	if err := idx.ReconcileFeedbackSignal(ctx, "acme", "api", legacyReaction); err != nil {
		t.Fatal(err)
	}
	if got := readRow(t, pool, install, legacyReactionID); got.deletedAt == nil {
		t.Fatal("legacy reaction dismissal was not retracted")
	}

	fb := base
	fb.Action = "dismissed"
	fb.Source = SourceReactionFeedback
	dismissedID := dismissalCustomIDForSource("api", fb.Category, fb.OriginalBody, SourceReactionFeedback)
	confirmedID := feedbackCustomIDForSource("acme", "api", fb.FilePath, fb.Category, fb.OriginalBody, "confirmed", SourceReactionFeedback)

	if err := idx.ReconcileFeedbackSignal(ctx, "acme", "api", fb); err != nil {
		t.Fatal(err)
	}
	if got := readRow(t, pool, install, dismissedID); got.deletedAt != nil {
		t.Fatal("reaction dismissal was not live")
	}

	fb.Action = "confirmed"
	if err := idx.ReconcileFeedbackSignal(ctx, "acme", "api", fb); err != nil {
		t.Fatal(err)
	}
	if got := readRow(t, pool, install, dismissedID); got.deletedAt == nil {
		t.Fatal("overturned reaction dismissal remains live and can still suppress")
	}
	if got := readRow(t, pool, install, confirmedID); got.deletedAt != nil {
		t.Fatal("reaction confirmation was not live")
	}
	for name, id := range map[string]string{"trusted reply": trustedID, "legacy trusted reply": legacyTrustedID} {
		if got := readRow(t, pool, install, id); got.deletedAt != nil {
			t.Fatalf("%s was deleted by reaction reconciliation", name)
		}
	}

	// Replay is idempotent and must resurrect neither stale state nor duplicates.
	if err := idx.ReconcileFeedbackSignal(ctx, "acme", "api", fb); err != nil {
		t.Fatal(err)
	}

	fb.Action = ""
	if err := idx.ReconcileFeedbackSignal(ctx, "acme", "api", fb); err != nil {
		t.Fatal(err)
	}
	if got := readRow(t, pool, install, confirmedID); got.deletedAt == nil {
		t.Fatal("neutral reaction tally did not retract reaction confirmation")
	}
	for name, id := range map[string]string{"trusted reply": trustedID, "legacy trusted reply": legacyTrustedID} {
		if got := readRow(t, pool, install, id); got.deletedAt != nil {
			t.Fatalf("%s was deleted by neutral reaction reconciliation", name)
		}
	}
	var liveReaction int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM memories
		WHERE installation_id = $1 AND custom_id IN ($2, $3) AND deleted_at IS NULL
	`, install, dismissedID, confirmedID).Scan(&liveReaction); err != nil {
		t.Fatal(err)
	}
	if liveReaction != 0 {
		t.Fatalf("live reaction feedback rows = %d, want 0", liveReaction)
	}
}
