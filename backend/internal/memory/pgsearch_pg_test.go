package memory

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
	pgxvector "github.com/pgvector/pgvector-go/pgx"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// routeEmbedder maps inputs to unit axis vectors so tests control cosine
// geometry exactly: same axis = similarity 1, different axes = 0.
type routeEmbedder struct {
	model string
	route func(string) int
}

func (r *routeEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		v := make([]float32, pgTestDims)
		v[r.route(in)] = 1
		out[i] = v
	}
	return out, nil
}

func (r *routeEmbedder) Model() string { return r.model }

// topicRoute puts goroutine-leak content on axis 1, injection content on
// axis 2, everything else (incl. placeholder queries) on axis 0.
func topicRoute(s string) int {
	switch {
	case strings.Contains(s, "goroutine leak"):
		return 1
	case strings.Contains(s, "sql injection"):
		return 2
	default:
		return 0
	}
}

func searchTestIndexer(t *testing.T) (*PGIndexer, context.Context) {
	t.Helper()
	pool, install := pgTestPool(t)
	emb := &routeEmbedder{model: "route-1024", route: topicRoute}
	idx := NewPGIndexer(pool, emb, install, pgTestDims, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	seed := []PatternMemory{
		{Content: "goroutine leak on shutdown path", Source: "pattern", Category: "bug_risk", FilePath: "srv.go"},
		{Content: "sql injection via string concat", Source: "pattern", Category: "security", FilePath: "db.go"},
	}
	for _, p := range seed {
		if _, err := idx.IndexPattern(ctx, "api", p); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return idx, ctx
}

// TestPGSearchScoreContract: Score is absolute cosine similarity, Threshold
// filters on it, and ranking surfaces the semantically-close doc first.
func TestPGSearchScoreContract(t *testing.T) {
	idx, ctx := searchTestIndexer(t)

	m, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 {
		t.Fatalf("threshold 0.5: got %d matches, want exactly the axis-aligned doc", len(m))
	}
	if !strings.Contains(m[0].Content, "goroutine leak") {
		t.Errorf("wrong doc surfaced: %q", m[0].Content)
	}
	if m[0].Score < 0.99 {
		t.Errorf("score = %f, want ~1.0 cosine (score contract: absolute similarity, not fused rank)", m[0].Score)
	}
	if m[0].Metadata["category"] != "bug_risk" {
		t.Errorf("metadata not carried: %v", m[0].Metadata)
	}
}

// TestPGSearchFTSLegSurfacesLexicalMatch: a doc semantically orthogonal to
// the query still surfaces through the FTS leg at Threshold 0 — and is
// correctly floored out when a similarity threshold applies.
func TestPGSearchFTSLegSurfacesLexicalMatch(t *testing.T) {
	idx, ctx := searchTestIndexer(t)

	m, err := idx.Search(ctx, MemoryQuery{
		Query: "string concat", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pm := range m {
		if strings.Contains(pm.Content, "sql injection") {
			found = true
		}
	}
	if !found {
		t.Fatal("FTS leg missed a lexical match at threshold 0")
	}

	// The other half of the contract the comment claims: the same lexical hit
	// is orthogonal in embedding space (score 0), so a positive floor must
	// drop it — a lexical match alone cannot attest similarity.
	floored, err := idx.Search(ctx, MemoryQuery{
		Query: "string concat", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pm := range floored {
		if strings.Contains(pm.Content, "sql injection") {
			t.Error("orthogonal lexical hit cleared a 0.5 similarity floor")
		}
	}
}

// TestPGSearchInvalidatedExcluded: invalidation removes a row from retrieval
// exactly like deletion (the suppression-path predicate).
func TestPGSearchInvalidatedExcluded(t *testing.T) {
	idx, ctx := searchTestIndexer(t)
	if _, err := idx.pool.Exec(ctx,
		"UPDATE memories SET invalidated_at = now() WHERE installation_id = $1 AND content LIKE '%goroutine leak%'",
		idx.installationID); err != nil {
		t.Fatal(err)
	}
	m, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pm := range m {
		if strings.Contains(pm.Content, "goroutine leak") {
			t.Fatal("invalidated row surfaced in search")
		}
	}
}

// TestPGSearchModelSpaceIsolation: rows embedded under another model are
// invisible to the vector leg (cross-space cosine is meaningless) and score 0
// — reachable only lexically at threshold 0.
func TestPGSearchModelSpaceIsolation(t *testing.T) {
	idx, ctx := searchTestIndexer(t)
	// Re-stamp one row as a foreign embedding space.
	if _, err := idx.pool.Exec(ctx,
		"UPDATE memories SET embedding_model = 'other-model' WHERE installation_id = $1 AND content LIKE '%goroutine leak%'",
		idx.installationID); err != nil {
		t.Fatal(err)
	}
	m, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("foreign-space row cleared a similarity floor it cannot attest: %+v", m)
	}
	m, err = idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) == 0 {
		t.Fatal("foreign-space row must stay lexically reachable at threshold 0")
	}
	if m[0].Score != 0 {
		t.Errorf("foreign-space score = %f, want 0", m[0].Score)
	}
}

// TestPGSearchNumericFilterAndScopeBoth: the shared-leg shape — numeric
// confidence floor plus ScopeBoth fan-out merging repo and _shared hits.
func TestPGSearchNumericFilterAndScopeBoth(t *testing.T) {
	idx, ctx := searchTestIndexer(t)
	if _, err := idx.IndexSharedPattern(ctx, PatternMemory{
		Content: "goroutine leak in worker pools", Source: "pattern", Category: "bug_risk",
	}); err != nil {
		t.Fatal(err)
	}

	m, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeBoth, Type: TypePattern,
		Filters: []FilterCondition{FilterNumeric("confidence", ">=", "0.5")},
		Limit:   5, Threshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only the shared doc carries confidence (pinned 1.00) — the repo doc
	// fails the numeric filter.
	if len(m) != 1 || !strings.Contains(m[0].Content, "worker pools") {
		t.Fatalf("numeric filter over fan-out: got %+v", m)
	}

	both, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeBoth, Type: TypePattern,
		Limit: 5, Threshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 2 {
		t.Fatalf("ScopeBoth merge: got %d, want repo + shared", len(both))
	}
}

// TestPGBriefingRendersSeededMemory: end-to-end briefing over the SQL core —
// synthesis point-lookup (threshold 0, metadata-pinned) plus semantic legs.
func TestPGBriefingRendersSeededMemory(t *testing.T) {
	idx, ctx := searchTestIndexer(t)
	if _, err := idx.IndexPattern(ctx, "api", PatternMemory{
		Content: "srv.go owns graceful shutdown; watch goroutine leak regressions",
		Source:  "synthesis", FilePath: "srv.go",
	}); err != nil {
		t.Fatal(err)
	}

	out, err := idx.Briefing(ctx, BriefingQuery{
		Owner: "acme", Repo: "api", FilePath: "srv.go", Query: "goroutine leak risk",
		// CharCap mirrors the production specialist call sites (2400) — the
		// renderer caps the body BEFORE appending the footer, and a zero cap
		// truncates everything (the gate caught this test shipping without it).
		Options: BriefingOptions{CharCap: 2400},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "graceful shutdown") {
		t.Errorf("briefing missing synthesis section:\n%s", out)
	}
	if !strings.Contains(out, "goroutine leak on shutdown path") {
		t.Errorf("briefing missing repo pattern leg:\n%s", out)
	}
}

// TestPGSearchEmbeddingsOffFTSOnly: nil embedder degrades to FTS-only — the
// synthesis point-lookup (threshold 0) still works, floors return empty.
func TestPGSearchEmbeddingsOffFTSOnly(t *testing.T) {
	pool, install := pgTestPool(t)
	seeded := NewPGIndexer(pool, &routeEmbedder{model: "route-1024", route: topicRoute}, install, pgTestDims, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	if _, err := seeded.IndexPattern(ctx, "api", PatternMemory{
		Content: "goroutine leak on shutdown path", Source: "pattern", Category: "bug_risk", FilePath: "srv.go",
	}); err != nil {
		t.Fatal(err)
	}

	off := NewPGIndexer(pool, nil, install, pgTestDims, slog.New(slog.DiscardHandler))
	m, err := off.Search(ctx, MemoryQuery{
		Query: "goroutine leak", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) == 0 {
		t.Fatal("embeddings-off FTS lookup returned nothing")
	}
	floored, err := off.Search(ctx, MemoryQuery{
		Query: "goroutine leak", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(floored) != 0 {
		t.Fatal("embeddings-off rows cannot attest a similarity floor")
	}
}

// countingEmbedder wraps routeEmbedder to count Embed calls (one call may
// carry a batch; every call is one provider round trip).
type countingEmbedder struct {
	inner *routeEmbedder
	mu    sync.Mutex
	calls int
}

func (c *countingEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.inner.Embed(ctx, inputs)
}

func (c *countingEmbedder) Model() string { return c.inner.Model() }

func (c *countingEmbedder) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestPGSearchEmbedsQueryOncePerFanOut: a ScopeBoth read embeds its query
// ONCE, not once per container leg — the fan-out doubled embedding-provider
// round trips on the hottest read paths (gate finding).
func TestPGSearchEmbedsQueryOncePerFanOut(t *testing.T) {
	pool, install := pgTestPool(t)
	emb := &countingEmbedder{inner: &routeEmbedder{model: "route-1024", route: topicRoute}}
	idx := NewPGIndexer(pool, emb, install, pgTestDims, slog.New(slog.DiscardHandler))
	ctx := context.Background()
	if _, err := idx.IndexPattern(ctx, "api", PatternMemory{
		Content: "goroutine leak on shutdown path", Source: "pattern", FilePath: "srv.go",
	}); err != nil {
		t.Fatal(err)
	}
	before := emb.count()

	if _, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeBoth, Type: TypePattern,
		Limit: 5, Threshold: 0.5,
	}); err != nil {
		t.Fatal(err)
	}
	if got := emb.count() - before; got != 1 {
		t.Errorf("ScopeBoth embedded the query %d times, want 1 (fan-out must share one embedding)", got)
	}
}

// TestPGSearchPinnedLookupReachesUnembeddedRow: the synthesis point-lookup
// (threshold 0 + pinning AND filters) must still find a row the fail-open
// write path left with a NULL embedding — its placeholder query never
// lexically matches synthesis prose, so both retrieval legs miss and only
// the predicate fallback can reach it (gate finding).
func TestPGSearchPinnedLookupReachesUnembeddedRow(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	// Write with embeddings OFF: the row lands with a NULL embedding.
	writer := NewPGIndexer(pool, nil, install, pgTestDims, slog.New(slog.DiscardHandler))
	if _, err := writer.IndexPattern(ctx, "api", PatternMemory{
		Content: "srv.go owns graceful shutdown; watch goroutine leak regressions",
		Source:  "synthesis", FilePath: "srv.go",
	}); err != nil {
		t.Fatal(err)
	}
	var embedded bool
	if err := pool.QueryRow(ctx,
		"SELECT embedding IS NOT NULL FROM memories WHERE installation_id = $1", install).Scan(&embedded); err != nil {
		t.Fatal(err)
	}
	if embedded {
		t.Fatal("precondition: fail-open write must leave a NULL embedding")
	}

	// Read with a working embedder — the exact specialist synthesis request.
	reader := NewPGIndexer(pool, &routeEmbedder{model: "route-1024", route: topicRoute}, install, pgTestDims, slog.New(slog.DiscardHandler))
	m, err := reader.Search(ctx, MemoryQuery{
		Query: "file synthesis", Repo: "api", Scope: ScopeRepo, Type: TypeSynthesis,
		Filters: []FilterCondition{{Key: "file_path", Value: "srv.go"}},
		Limit:   1, Threshold: 0, PointLookup: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 {
		t.Fatalf("pinned point-lookup lost an unembedded row: got %d matches", len(m))
	}
	if !strings.Contains(m[0].Content, "graceful shutdown") {
		t.Errorf("wrong row: %q", m[0].Content)
	}
}

// TestPGSearchNumericFilterTolagesMalformedValue: one row carrying a
// non-numeric value under a numerically-filtered key must simply not match —
// not raise 22P02 and fail the whole leg (which, through the fan-out's
// single-error policy, would brick every confidence-floored briefing read
// after a backfill wrote one malformed legacy doc). Gate finding.
func TestPGSearchNumericFilterToleratesMalformedValue(t *testing.T) {
	idx, ctx := searchTestIndexer(t)
	if _, err := idx.IndexSharedPattern(ctx, PatternMemory{
		Content: "goroutine leak in worker pools", Source: "pattern",
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate the backfill hazard: a legacy doc whose confidence is prose.
	if _, err := idx.pool.Exec(ctx,
		`UPDATE memories SET metadata = jsonb_set(metadata, '{confidence}', '"high"')
		 WHERE installation_id = $1 AND container_tag = $2`,
		idx.installationID, SharedTag); err != nil {
		t.Fatal(err)
	}

	m, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeBoth, Type: TypePattern,
		Filters: []FilterCondition{FilterNumeric("confidence", ">=", "0.5")},
		Limit:   5, Threshold: 0.5,
	})
	if err != nil {
		t.Fatalf("malformed metadata errored the search instead of not matching: %v", err)
	}
	for _, pm := range m {
		if strings.Contains(pm.Content, "worker pools") {
			t.Error("row with non-numeric confidence matched a numeric floor")
		}
	}
}

// TestPGSearchStringContainsIsLiteral: LIKE metacharacters in a filter value
// are escaped — "%" must match a literal percent, not every row.
func TestPGSearchStringContainsIsLiteral(t *testing.T) {
	idx, ctx := searchTestIndexer(t)
	m, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Filters: []FilterCondition{{Key: "file_path", Value: "%", FilterType: "string_contains"}},
		Limit:   5, Threshold: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Errorf("wildcard-as-literal matched %d rows; '%%' must be a literal substring", len(m))
	}
}

// gradedEmbedder places the query on axis 0 and each seeded doc at a
// caller-chosen cosine to it, so a test can pit similarity against lexical
// overlap deterministically.
type gradedEmbedder struct{ cos map[string]float64 }

func (g *gradedEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		v := make([]float32, pgTestDims)
		c, ok := g.cos[in]
		if !ok {
			c = 1 // the query itself, and anything unregistered
		}
		// Unit vector in the (axis0, axis1) plane at the requested cosine.
		v[0] = float32(c)
		v[1] = float32(math.Sqrt(math.Max(0, 1-c*c)))
		out[i] = v
	}
	return out, nil
}

func (g *gradedEmbedder) Model() string { return "graded-1024" }

// TestPGSearchEmitsInSimilarityOrder is the gate's blocking repro: a
// lexically-identical low-similarity doc wins RRF fusion, but every reader
// adapter above this seam is a top-N-BY-SIMILARITY consumer (BestMatch,
// TopContent, ScenarioResults) gating on absolute cosine floors. If the
// LIMIT is applied in fused-rank order, a Limit-1 read returns 0.55 while a
// 0.95 row sits in the pool — attribution (0.80) never fires and a finding
// that should be suppressed (0.95) posts.
func TestPGSearchEmitsInSimilarityOrder(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	const (
		query    = "nullpointer dereference in handler"
		semantic = "AAA unrelated vocabulary zeta omicron"  // no lexical overlap
		lexical  = "BBB nullpointer dereference in handler" // exact lexical match
	)
	emb := &gradedEmbedder{cos: map[string]float64{semantic: 0.95, lexical: 0.55, query: 1}}
	idx := NewPGIndexer(pool, emb, install, pgTestDims, slog.New(slog.DiscardHandler))
	for _, c := range []string{semantic, lexical} {
		if _, err := idx.IndexPattern(ctx, "api", PatternMemory{Content: c, Source: "pattern"}); err != nil {
			t.Fatal(err)
		}
	}

	full, err := idx.Search(ctx, MemoryQuery{
		Query: query, Repo: "api", Scope: ScopeRepo, Type: TypePattern, Limit: 10, Threshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 2 {
		t.Fatalf("both docs should clear a 0.5 floor: got %d", len(full))
	}
	if !strings.Contains(full[0].Content, "AAA") {
		t.Errorf("results not similarity-descending: [0] = %q (score %.3f), want the 0.95 doc",
			full[0].Content, full[0].Score)
	}

	// The enricher's real shape: Limit 1 then BestMatch, gated at 0.80.
	top, err := idx.Search(ctx, MemoryQuery{
		Query: query, Repo: "api", Scope: ScopeRepo, Type: TypePattern, Limit: 1, Threshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 {
		t.Fatalf("Limit 1: got %d", len(top))
	}
	if best := BestMatch(top...); best.Score < 0.8 {
		t.Errorf("Limit-1 read returned score %.3f (%q); the repo holds a 0.95 match, so the 0.80 attribution floor can never fire",
			best.Score, best.Content)
	}
}

// TestPGSearchPinnedFallbackRunsInCallerTx is the gate's second blocking
// repro: the point-lookup fallback used to acquire a SECOND pool connection
// while the hybrid leg's read-only transaction still held the first. Under
// fan-out that exhausts the shared pool (MaxConns 20 vs 10 concurrent
// reviews) and every caller — memory or not — stalls to its deadline. A
// single-connection pool makes the self-deadlock deterministic.
func TestPGSearchPinnedFallbackRunsInCallerTx(t *testing.T) {
	shared, install := pgTestPool(t)
	ctx := context.Background()
	// Fail-open write: NULL embedding, so both retrieval legs must miss and
	// only the pinned fallback can reach the row.
	writer := NewPGIndexer(shared, nil, install, pgTestDims, slog.New(slog.DiscardHandler))
	if _, err := writer.IndexPattern(ctx, "api", PatternMemory{
		Content: "srv.go owns graceful shutdown", Source: "synthesis", FilePath: "srv.go",
	}); err != nil {
		t.Fatal(err)
	}

	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvector.RegisterTypes(ctx, conn)
	}
	one, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()

	idx := NewPGIndexer(one, &routeEmbedder{model: "route-1024", route: topicRoute}, install, pgTestDims, slog.New(slog.DiscardHandler))
	deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	m, err := idx.Search(deadline, MemoryQuery{
		Query: "file synthesis", Repo: "api", Scope: ScopeRepo, Type: TypeSynthesis,
		Filters: []FilterCondition{{Key: "file_path", Value: "srv.go"}},
		Limit:   1, Threshold: 0, PointLookup: true,
	})
	if err != nil {
		t.Fatalf("pinned fallback deadlocked on the caller's own connection: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("pinned fallback lost the row: got %d matches", len(m))
	}
}

// TestPGSearchNaNScoreCannotClearFloor: a degenerate all-zero stored vector
// makes cosine distance NaN, and Postgres orders NaN ABOVE every real value
// (NaN >= 0.9 is TRUE, GREATEST(NaN,0) stays NaN, LEAST(GREATEST(NaN,0),1)
// yields a perfect 1). Such a row would clear every similarity floor, sort
// first, and have its content copied verbatim into specialist briefings —
// a memory-poisoning primitive. It must score 0 like NULL/foreign-model rows.
func TestPGSearchNaNScoreCannotClearFloor(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, &routeEmbedder{model: "route-1024", route: topicRoute}, install, pgTestDims, slog.New(slog.DiscardHandler))

	// The poisoned row must reach the candidate pool for the score gate to be
	// exercised at all; make it a lexical match so the FTS leg carries it in
	// deterministically, independent of HNSW graph reachability.
	const poisoned = "goroutine leak risk note with a degenerate embedding"
	if _, err := idx.IndexPattern(ctx, "api", PatternMemory{Content: poisoned, Source: "pattern"}); err != nil {
		t.Fatal(err)
	}
	// Bypass the write-side guard to simulate a row already in the table.
	zero := make([]float32, pgTestDims)
	tag, err := idx.pool.Exec(ctx,
		"UPDATE memories SET embedding = $1 WHERE installation_id = $2 AND content = $3",
		pgvector.NewVector(zero), idx.installationID, poisoned)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("precondition: zeroed %d rows, want 1", tag.RowsAffected())
	}

	m, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0.9,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pm := range m {
		if pm.Content == poisoned {
			t.Fatalf("zero-vector row cleared a 0.9 floor with score %v — NaN outranks every real score in Postgres", pm.Score)
		}
	}
}

// TestPGIndexerRejectsZeroVector: the write side must never create such a row
// in the first place — dimensionality alone does not catch a degenerate
// embedding, so a stubbed custom endpoint returning zeros would poison the
// corpus. Fail open to NULL, like the other embedder-contract violations.
func TestPGIndexerRejectsZeroVector(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	idx := NewPGIndexer(pool, &zeroEmbedder{}, install, pgTestDims, slog.New(slog.DiscardHandler))
	if _, err := idx.IndexPattern(ctx, "api", PatternMemory{Content: "x", Source: "pattern"}); err != nil {
		t.Fatal(err)
	}
	var embedded bool
	if err := pool.QueryRow(ctx,
		"SELECT embedding IS NOT NULL FROM memories WHERE installation_id = $1", install).Scan(&embedded); err != nil {
		t.Fatal(err)
	}
	if embedded {
		t.Error("a zero vector was stored; it must fail open to NULL for backfill")
	}
}

type zeroEmbedder struct{}

func (zeroEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = make([]float32, pgTestDims)
	}
	return out, nil
}

func (zeroEmbedder) Model() string { return "zero-1024" }

// TestPGSearchNULInQueryDoesNotFail: Postgres TEXT rejects NUL (22021), and
// searchMemory feeds req.Query straight from LLM tool-call arguments, which
// are steerable by text in an untrusted PR diff. The write path already
// strips NUL; the read path must too, or one stray byte fails the search --
// and a failed dismissal read reposts a finding the developer dismissed.
func TestPGSearchNULInQueryDoesNotFail(t *testing.T) {
	idx, ctx := searchTestIndexer(t)
	m, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak\x00risk", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0,
	})
	if err != nil {
		t.Fatalf("NUL in query failed the whole read: %v", err)
	}
	if len(m) == 0 {
		t.Error("scrubbed query should still retrieve the seeded pattern")
	}
}

// TestPGSearchRejectsZeroQueryVector: the read side must enforce the same
// contract as the write side. A degenerate embedder returns a correctly-sized,
// correctly-counted, correctly-model-stamped ZERO vector; cosine is then NaN
// against every row, the NaN guard scores them all 0, and every
// similarity-floored read returns EMPTY with a nil error. Callers cannot tell
// that from a genuine no-match, so the enricher marks every finding novel and
// dismissal suppression silently stops. It must be an error, not a degrade.
func TestPGSearchRejectsZeroQueryVector(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	seeded := NewPGIndexer(pool, &routeEmbedder{model: "route-1024", route: topicRoute}, install, pgTestDims, slog.New(slog.DiscardHandler))
	if _, err := seeded.IndexPattern(ctx, "api", PatternMemory{
		Content: "goroutine leak on shutdown path", Source: "pattern",
	}); err != nil {
		t.Fatal(err)
	}

	degenerate := NewPGIndexer(pool, zeroEmbedder{}, install, pgTestDims, slog.New(slog.DiscardHandler))
	_, err := degenerate.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0.85,
	})
	if err == nil {
		t.Fatal("zero query vector returned empty with a nil error; callers read that as a genuine no-match")
	}
}

// TestPGSearchNonNumericFilterValueDoesNotError: the numeric filter guards the
// stored value against 22P02 — the ARGUMENT needs the same guard, or an
// unparseable filter value fails the whole leg (and, via the fan-out's
// single-error policy, the whole read).
func TestPGSearchNonNumericFilterValueDoesNotError(t *testing.T) {
	idx, ctx := searchTestIndexer(t)
	m, err := idx.Search(ctx, MemoryQuery{
		Query: "goroutine leak risk", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Filters: []FilterCondition{FilterNumeric("confidence", ">=", "0 OR true")},
		Limit:   5, Threshold: 0,
	})
	if err != nil {
		t.Fatalf("non-numeric filter value errored the read instead of matching nothing: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("unparseable numeric filter should match nothing, got %d rows", len(m))
	}
}

// TestPGSearchTenantIsolation is the guard the whole PG read path lacked:
// every other PG test uses ONE installation, so mutating
// `installation_id = $1` to `(installation_id = $1 OR true)` — or dropping
// the shared predicate prefix from the FTS leg alone — left the entire suite
// green while leaking one customer's memory into another's review.
func TestPGSearchTenantIsolation(t *testing.T) {
	pool, installA := pgTestPool(t)
	ctx := context.Background()
	var installB int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES (910_061, 'pgsearch-tenant-b')
		ON CONFLICT (installation_id) DO UPDATE SET org_login = EXCLUDED.org_login
		RETURNING id`).Scan(&installB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM memories WHERE installation_id = $1", installB); err != nil {
			t.Errorf("cleanup tenant B: %v", err)
		}
	})

	const secret = "goroutine leak SECRETCUSTOMERTOKEN in victim tenant"
	emb := &routeEmbedder{model: "route-1024", route: topicRoute}
	victim := NewPGIndexer(pool, emb, installB, pgTestDims, slog.New(slog.DiscardHandler))
	if _, err := victim.IndexPattern(ctx, "api", PatternMemory{Content: secret, Source: "pattern"}); err != nil {
		t.Fatal(err)
	}

	// Tenant A: same repo name, same query, both retrieval legs would match.
	attacker := NewPGIndexer(pool, emb, installA, pgTestDims, slog.New(slog.DiscardHandler))
	m, err := attacker.Search(ctx, MemoryQuery{
		Query: "goroutine leak", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 10, Threshold: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pm := range m {
		if strings.Contains(pm.Content, "SECRETCUSTOMERTOKEN") {
			t.Fatalf("CROSS-TENANT LEAK: installation A read installation B's memory (score %v)", pm.Score)
		}
	}

	// The briefing path composes several legs — cover it too.
	out, err := attacker.Briefing(ctx, BriefingQuery{
		Owner: "acme", Repo: "api", FilePath: "srv.go", Query: "goroutine leak",
		Options: BriefingOptions{CharCap: 2400},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "SECRETCUSTOMERTOKEN") {
		t.Fatal("CROSS-TENANT LEAK: tenant B memory rendered into tenant A's briefing")
	}
}

// TestPGSearchFTSLegIsLoadBearing isolates the lexical leg: the row has NO
// same-space embedding, so the vector CTE cannot return it, and the read is
// not a PointLookup, so the predicate fallback is ineligible. Only the FTS
// leg can surface it. Neutering that leg previously left every PG test green.
func TestPGSearchFTSLegIsLoadBearing(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	// Fail-open write: NULL embedding.
	writer := NewPGIndexer(pool, nil, install, pgTestDims, slog.New(slog.DiscardHandler))
	if _, err := writer.IndexPattern(ctx, "api", PatternMemory{
		Content: "goroutine leak on shutdown path", Source: "pattern",
	}); err != nil {
		t.Fatal(err)
	}

	reader := NewPGIndexer(pool, &routeEmbedder{model: "route-1024", route: topicRoute}, install, pgTestDims, slog.New(slog.DiscardHandler))
	m, err := reader.Search(ctx, MemoryQuery{
		Query: "goroutine leak", Repo: "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 {
		t.Fatalf("FTS leg is the only path to an unembedded row: got %d matches", len(m))
	}
	if m[0].Score != 0 {
		t.Errorf("unembedded row scored %v, want 0 (it cannot attest similarity)", m[0].Score)
	}
}

// TestPGSearchNoFallbackOnRankingRead: the point-lookup fallback must fire
// ONLY when the caller declares the read a lookup. A ranking read (scenario
// search sets no threshold and filters only on type) whose legs both miss
// must return EMPTY — not arbitrary newest rows at score 0, which briefing
// rendering would copy verbatim into a review prompt with no score gate.
func TestPGSearchNoFallbackOnRankingRead(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	writer := NewPGIndexer(pool, nil, install, pgTestDims, slog.New(slog.DiscardHandler))
	if _, err := writer.IndexPattern(ctx, "api", PatternMemory{
		Content: "Never call time.Sleep inside a request handler", Source: "pattern",
	}); err != nil {
		t.Fatal(err)
	}

	reader := NewPGIndexer(pool, &routeEmbedder{model: "route-1024", route: topicRoute}, install, pgTestDims, slog.New(slog.DiscardHandler))
	m, err := reader.Search(ctx, MemoryQuery{
		// Zero lexical overlap and no same-space embedding: both legs miss.
		Query: "unrelated cryptography key rotation question",
		Repo:  "api", Scope: ScopeRepo, Type: TypePattern,
		Limit: 5, Threshold: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("ranking read fell back to a predicate scan and returned %d unrelated row(s) at score %v — that content would be injected verbatim into a specialist prompt",
			len(m), m[0].Score)
	}
}

// TestPGSearchPointLookupHonoursThreshold: the fallback projects score 0, so
// firing it under a positive floor would hand back a row the caller's own
// gate rejects — and TopContent/HintStrings copy content with no score
// re-check. Not reachable from today's only caller (Threshold 0), but
// MemoryQuery.PointLookup is a public seam field now.
func TestPGSearchPointLookupHonoursThreshold(t *testing.T) {
	pool, install := pgTestPool(t)
	ctx := context.Background()
	writer := NewPGIndexer(pool, nil, install, pgTestDims, slog.New(slog.DiscardHandler))
	if _, err := writer.IndexPattern(ctx, "api", PatternMemory{
		Content: "srv.go owns graceful shutdown", Source: "synthesis", FilePath: "srv.go",
	}); err != nil {
		t.Fatal(err)
	}

	reader := NewPGIndexer(pool, &routeEmbedder{model: "route-1024", route: topicRoute}, install, pgTestDims, slog.New(slog.DiscardHandler))
	m, err := reader.Search(ctx, MemoryQuery{
		Query: "file synthesis", Repo: "api", Scope: ScopeRepo, Type: TypeSynthesis,
		Filters: []FilterCondition{{Key: "file_path", Value: "srv.go"}},
		Limit:   1, Threshold: 0.85, PointLookup: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pm := range m {
		if pm.Score < 0.85 {
			t.Fatalf("point-lookup fallback returned score %v below the caller's 0.85 floor: %q", pm.Score, pm.Content)
		}
	}
}
