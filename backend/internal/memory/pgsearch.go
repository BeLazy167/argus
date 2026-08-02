package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	pgvector "github.com/pgvector/pgvector-go"
)

// scrubNUL strips NUL from every string this package binds into SQL.
// Postgres TEXT rejects 0x00 (22021) and the write path already strips it, so
// the read path must match — one stray byte otherwise fails the whole search,
// and because the dismissal read degrades via BestEffort that silently
// reposts a finding the developer already dismissed.
func scrubNUL(req SearchRequest) SearchRequest {
	const nul = "\x00"
	req.Query = strings.ReplaceAll(req.Query, nul, "")
	req.ContainerTag = strings.ReplaceAll(req.ContainerTag, nul, "")
	if req.Filters != nil {
		f := *req.Filters
		clean := func(in []FilterCondition) []FilterCondition {
			if in == nil {
				return nil
			}
			out := make([]FilterCondition, len(in))
			for i, c := range in {
				c.Key = strings.ReplaceAll(c.Key, nul, "")
				c.Value = strings.ReplaceAll(c.Value, nul, "")
				out[i] = c
			}
			return out
		}
		f.AND, f.OR = clean(f.AND), clean(f.OR)
		req.Filters = &f
	}
	return req
}

// embedQuery embeds one query string, or returns (nil, nil) when embeddings
// are off (nil embedder) — the FTS-only degrade.
func (idx *PGIndexer) embedQuery(ctx context.Context, query string) (*pgvector.Vector, error) {
	if idx.embedder == nil {
		return nil, nil
	}
	vecs, err := idx.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embedding search query: %w", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != idx.dims {
		return nil, fmt.Errorf("embedding search query: provider returned %d vectors (dims %d, want %d)",
			len(vecs), lenFirst(vecs), idx.dims)
	}
	// Same contract the write path enforces: a zero query vector has no
	// direction, so cosine is NaN against every row, the NaN guard scores
	// them all 0, and every similarity-floored read returns EMPTY with a nil
	// error. Callers cannot tell that from a genuine no-match — the enricher
	// would mark every finding novel and dismissal suppression would silently
	// stop. The reader seam is error-honest, so this is an error, not a
	// degrade.
	if allZero(vecs[0]) {
		return nil, fmt.Errorf("embedding search query: provider returned a zero vector (no direction; every similarity would be undefined)")
	}
	v := pgvector.NewVector(vecs[0])
	return &v, nil
}

// The Postgres transport core behind the shared read orchestration
// (searchWith / specialistBlockWith / briefingWith): one container, one
// SearchRequest, hybrid retrieval in a single SQL round trip. memoRunSearch
// wraps it with per-query embedding memoization; runSearchVec is the SQL.
//
// RRF (k=60) decides CANDIDACY, not emission order. Both legs contribute up
// to `pool` rows; fusion picks what competes — a lexically-strong row the
// vector leg ranked past `pool` still enters. But rows are EMITTED in
// similarity order (score DESC, rrf DESC as tie-break), because every reader
// adapter above this seam is a top-N-by-similarity consumer: BestMatch,
// TopContent, ScenarioResults (reader.go: "Matches arrive already sorted by
// similarity descending"), and every caller gates on an ABSOLUTE cosine floor
// (Attribution 0.80, SuppressionDrop 0.85, ScenarioDedupe 0.85). Ordering the
// LIMIT by fused rank instead let a lexically-similar 0.55 row displace a
// 0.95 row from a Limit-1 read — the floor then never fires, attribution
// credits the wrong pattern, and a finding that should be suppressed posts.
// The RRF value is still normalized to [0,1] by the two-leg maximum 2/(k+1)
// so a future additive recency term cannot swamp it.
//
// Score contract (migration 057 header): PatternMatch.Score is the absolute
// cosine similarity 1-(embedding<=>query), clamped to [0,1]. The clamp needs
// an explicit NaN guard, not just GREATEST: a degenerate (all-zero) stored
// vector makes the distance NaN, Postgres orders NaN ABOVE every real value
// so `NaN >= threshold` is TRUE, and GREATEST(NaN,0) stays NaN — such a row
// would clear every floor AND sort first, and its content is copied verbatim
// into specialist briefings. (LEAST(GREATEST(NaN,0),1) is worse still: it
// yields 1, a perfect score.) NaN scores 0, like NULL and foreign-model rows:
// it cannot attest similarity. Raw cosine
// spans [-1,1], and an unclamped negative would make `score >= 0` drop a
// row the FTS leg legitimately surfaced at threshold 0 (anti-correlated
// embedding, lexical match). Fusion reorders, never
// replaces the score, and Threshold filters on it. Rows without a
// same-space embedding (NULL pending backfill, or stamped by another model —
// cross-space cosine is meaningless) score 0: they can never clear a
// positive similarity floor. Do NOT rely on the FTS leg to reach them:
// websearch_to_tsquery is CONJUNCTIVE, so a multi-term query only matches
// docs containing every lexeme, and production queries are whole finding
// texts — the lexical leg contributes nothing on those reads (tracked for
// the PR-7 retune, where shadow data can price the alternatives). A
// threshold-0 read carrying pinning AND filters (the synthesis
// point-lookup) falls back to a plain predicate scan when both legs miss —
// without it, a fail-open write (NULL embedding) would silently drop File
// History from briefings until backfill, since the point-lookup's
// placeholder query never lexically matches synthesis prose.
//
// With no embedder (embeddings off) the vector leg is skipped entirely —
// FTS-only, same deliberate degrade as the fail-open write path. A query
// embed FAILURE, by contrast, returns the error: the reader seam is
// error-honest and callers own the degrade policy (BestEffort or propagate).
//
// Rerank is a no-op here (Supermemory's server-side reranker; the SQL
// ordering is already exact over the candidate pool). Enrich has no
// related-memories graph to pull, so RichContent carries Content — the
// downstream shaping (HintStrings' 500-char truncation) is unchanged.
// memoRunSearch returns the transport core for ONE logical read, memoizing
// query embeddings per distinct string: a ScopeBoth fan-out embeds its query
// once instead of once per container leg, and a review briefing embeds its 3
// distinct strings 3 times instead of 5 — halving embedding-provider load
// and latency on the hottest read paths. Distinct queries embed concurrently
// (per-query sync.Once); legs sharing a query wait instead of re-embedding.
// The memo dies with the call, so nothing goes stale.
func (idx *PGIndexer) memoRunSearch() runSearchFn {
	type qmemo struct {
		mu   sync.Mutex
		vec  *pgvector.Vector
		done bool
	}
	var mu sync.Mutex
	memos := map[string]*qmemo{}
	get := func(q string) *qmemo {
		mu.Lock()
		defer mu.Unlock()
		m, ok := memos[q]
		if !ok {
			m = &qmemo{}
			memos[q] = m
		}
		return m
	}
	return func(ctx context.Context, req SearchRequest) ([]PatternMatch, error) {
		m := get(req.Query)
		m.mu.Lock()
		if !m.done {
			vec, err := idx.embedQuery(ctx, req.Query)
			if err != nil {
				// Deliberately NOT memoized. Legs sharing a query carry
				// independently-scoped deadlines (each briefing leg owns its
				// own 5s), so caching the losing goroutine's cancellation
				// would replay one leg's timeout as a hard error to a leg
				// whose own context is still alive — re-coupling exactly what
				// the per-leg timeouts exist to separate. Only success is
				// shared; a failure lets the next caller retry on its own ctx.
				m.mu.Unlock()
				return nil, err
			}
			m.vec, m.done = vec, true
		}
		vec := m.vec
		m.mu.Unlock()
		return idx.runSearchVec(ctx, req, vec)
	}
}

func (idx *PGIndexer) runSearchVec(ctx context.Context, req SearchRequest, qv *pgvector.Vector) ([]PatternMatch, error) {
	// Postgres TEXT rejects NUL (22021). The write path already strips it
	// (pgindexer.go), and the read path must match: req.Query reaches
	// websearch_to_tsquery as a bound parameter, and searchMemory feeds it
	// straight from LLM tool-call arguments, which are steerable by text in
	// an untrusted PR diff. Unscrubbed, one \u0000 fails the whole search —
	// and a failed dismissal read means a 👎-dismissed finding gets reposted.
	req = scrubNUL(req)
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	// Candidate pool per leg: wide enough that fusion has real overlap to
	// work with, aligned with ef_search=80 (recall study: ef 80 ≈ 0.95+
	// recall at this scale; pool caps what fusion can see).
	pool := 4 * limit
	if pool < 40 {
		pool = 40
	}

	where, args := idx.searchPredicates(req)
	var rows pgx.Rows
	tx, err := idx.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("memory search begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if qv != nil {
		// ef_search=80 (SOTA-validated over the 40 default) and relaxed
		// iterative scan so a filtered HNSW walk keeps yielding candidates
		// instead of stopping short of the pool size.
		if _, err := tx.Exec(ctx, "SET LOCAL hnsw.ef_search = 80"); err != nil {
			return nil, fmt.Errorf("memory search tuning: %w", err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL hnsw.iterative_scan = 'relaxed_order'"); err != nil {
			return nil, fmt.Errorf("memory search tuning: %w", err)
		}
		args = append(args, qv, idx.embedder.Model(), req.Query, pool, req.Threshold, limit)
		n := len(args)
		q := fmt.Sprintf(`
WITH vec AS (
  SELECT id, row_number() OVER (ORDER BY embedding <=> $%[1]d) AS rnk
  FROM memories
  WHERE %[7]s AND embedding IS NOT NULL AND embedding_model = $%[2]d
  ORDER BY embedding <=> $%[1]d
  LIMIT $%[4]d
), fts AS (
  SELECT id, row_number() OVER (ORDER BY ts_rank_cd(content_tsv, websearch_to_tsquery('english', $%[3]d)) DESC, id) AS rnk
  FROM memories
  WHERE %[7]s AND content_tsv @@ websearch_to_tsquery('english', $%[3]d)
  ORDER BY ts_rank_cd(content_tsv, websearch_to_tsquery('english', $%[3]d)) DESC, id
  LIMIT $%[4]d
), fused AS (
  SELECT COALESCE(v.id, f.id) AS id,
         (COALESCE(1.0/(60+v.rnk), 0) + COALESCE(1.0/(60+f.rnk), 0)) / (2.0/61.0) AS rrf
  FROM vec v FULL OUTER JOIN fts f USING (id)
), scored AS (
  SELECT m.custom_id, m.content, m.metadata, u.rrf,
         CASE WHEN m.embedding IS NOT NULL AND m.embedding_model = $%[2]d
                   AND NOT ((m.embedding <=> $%[1]d) = 'NaN'::float8)
              THEN LEAST(GREATEST(1 - (m.embedding <=> $%[1]d), 0::float8), 1::float8) ELSE 0 END AS score
  FROM fused u JOIN memories m ON m.id = u.id
)
SELECT custom_id, content, metadata, score
FROM scored
WHERE score >= $%[5]d::float8
ORDER BY score DESC, rrf DESC, custom_id
LIMIT $%[6]d`,
			n-5, n-4, n-3, n-2, n-1, n, where)
		rows, err = tx.Query(ctx, q, args...)
	} else {
		// Score contract: without a query vector every row scores 0, so a
		// positive floor is unsatisfiable — return empty without a round
		// trip. (This also keeps every remaining SQL parameter in an
		// unambiguous type context: the previous `0 >= $threshold` gate let
		// Postgres infer the parameter as integer, an ambiguous bind that
		// misbehaved on CI.)
		if req.Threshold > 0 {
			// Every production read carries a floor (FindingEnrich 0.50,
			// SpecialistMin 0.60, SuppressionDrop 0.85), so with embeddings
			// off this returns empty for all of them: memory is effectively
			// inert, every finding reads as novel, and suppression stops.
			// That is intended, but it must not look like a healthy no-match.
			idx.logger.Warn("embeddings off: every similarity-floored memory read returns empty",
				"container", req.ContainerTag, "threshold", req.Threshold)
			logSearchResult(idx.logger, req, 0)
			return nil, nil
		}
		args = append(args, req.Query, limit)
		n := len(args)
		q := fmt.Sprintf(`
SELECT custom_id, content, metadata, 0::float8 AS score
FROM memories
WHERE %[3]s AND content_tsv @@ websearch_to_tsquery('english', $%[1]d)
ORDER BY ts_rank_cd(content_tsv, websearch_to_tsquery('english', $%[1]d)) DESC, id
LIMIT $%[2]d`,
			n-1, n, where)
		rows, err = tx.Query(ctx, q, args...)
	}
	if err != nil {
		return nil, fmt.Errorf("memory search: %w", err)
	}
	defer rows.Close()

	out, err := scanMatches(rows, req, "memory search")
	if err != nil {
		return nil, err
	}
	// Point-lookup fallback: a read the CALLER declared to be a lookup (its
	// AND filters uniquely pin a row) is not a ranking problem — when both
	// retrieval legs miss (NULL embedding from the fail-open write path,
	// prose that never matches the placeholder query), the pinned row must
	// still be reachable. Gated on the explicit flag, never inferred: the
	// filter-shape heuristic this replaced also matched ordinary ranking
	// reads, where falling back returns arbitrary newest rows at score 0 —
	// and briefing rendering copies that content verbatim into a review
	// prompt with no score gate.
	// req.Threshold <= 0 is part of the gate, not an afterthought: the
	// fallback projects score 0, so firing it under a positive floor would
	// hand back a row the caller's own gate rejects — and TopContent /
	// HintStrings copy content with no score re-check. The nil-embedder
	// branch above already returns empty under a floor; this keeps the two
	// branches agreeing.
	if len(out) == 0 && req.PointLookup && req.Threshold <= 0 {
		pw, pa := idx.searchPredicates(req)
		out, err = pinnedScan(ctx, tx, pw, pa, req, limit)
		if err != nil {
			return nil, err
		}
	}
	logSearchResult(idx.logger, req, len(out))
	return out, nil
}

// pinnedScan is the predicate-only leg behind the point-lookup fallback:
// same WHERE as the hybrid legs (tenant, container, tombstones, filters),
// no lexical or vector requirement, newest first, score 0.
// pgQuerier is satisfied by both *pgxpool.Pool and pgx.Tx. The fallback takes
// it explicitly so it can run INSIDE the caller's open transaction: acquiring
// a second connection from the shared pool while the first is still held
// self-deadlocks under fan-out — with MaxConns 20 and MAX_CONCURRENT_REVIEWS
// 10, ten briefings taking this fallback hold ten transactions and block on
// ten more Acquires, exhausting the pool for the whole app until every
// caller's deadline expires. The fallback is the NORMAL path during rollout
// (NULL embeddings pending backfill), so this was reachable, not theoretical.
type pgQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func pinnedScan(ctx context.Context, q pgQuerier, where string, args []any, req SearchRequest, limit int) ([]PatternMatch, error) {
	args = append(args, limit)
	sql := fmt.Sprintf(`
SELECT custom_id, content, metadata, 0::float8 AS score
FROM memories
WHERE %s
ORDER BY updated_at DESC, id DESC
LIMIT $%d`, where, len(args))
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("memory pinned scan: %w", err)
	}
	defer rows.Close()
	return scanMatches(rows, req, "memory pinned scan")
}

// scanMatches converts the shared 4-column projection (custom_id, content,
// metadata, score) into matches. Both SQL shapes and the pinned fallback
// project identically, so the row->match contract lives here once: a
// malformed metadata blob degrades to nil Metadata rather than failing the
// read, and enrichment (no related-memories graph in PG) carries Content.
func scanMatches(rows pgx.Rows, req SearchRequest, op string) ([]PatternMatch, error) {
	var out []PatternMatch
	for rows.Next() {
		var pm PatternMatch
		var metaJSON []byte
		if err := rows.Scan(&pm.ID, &pm.Content, &metaJSON, &pm.Score); err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		if len(metaJSON) > 0 {
			var md map[string]string
			if err := json.Unmarshal(metaJSON, &md); err == nil {
				pm.Metadata = md
			}
		}
		if req.Include != nil {
			pm.RichContent = pm.Content
		}
		out = append(out, pm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}

// searchPredicates renders the shared WHERE prefix both legs use: tenant,
// container, live-row tombstones, and the request's filter groups. Filter
// semantics mirror the Supermemory server: the AND group must all hold, the
// OR group needs at least one, both ANDed together when present. The `type`
// key reads the indexed column (Doc.Type == metadata["type"] by
// construction); every other key reads the metadata map.
func (idx *PGIndexer) searchPredicates(req SearchRequest) (string, []any) {
	args := []any{idx.installationID, req.ContainerTag}
	conds := []string{
		"installation_id = $1",
		"container_tag = $2",
		"deleted_at IS NULL",
		"invalidated_at IS NULL",
	}
	if req.Filters != nil {
		for _, f := range req.Filters.AND {
			frag, a := filterSQL(f, len(args))
			conds = append(conds, frag)
			args = append(args, a...)
		}
		if len(req.Filters.OR) > 0 {
			var ors []string
			for _, f := range req.Filters.OR {
				frag, a := filterSQL(f, len(args))
				ors = append(ors, frag)
				args = append(args, a...)
			}
			conds = append(conds, "("+strings.Join(ors, " OR ")+")")
		}
	}
	return strings.Join(conds, " AND "), args
}

// numericLiteralPattern is the SINGLE source for both ::numeric guards: the
// Go-side check on the argument and the SQL-side check on the stored value.
// They were separately-maintained string literals that happened to agree; one
// being loosened (or "simplified" to something like `^[0-9.-]+$`, which
// accepts "1.2.3") re-admits the 22P02 that fails the whole leg. Valid in both
// Go's regexp and POSIX ERE, and contains no single quote to escape.
const numericLiteralPattern = `^-?[0-9]+(\.[0-9]+)?$`

// numericLiteral matches what Postgres will accept for the ::numeric casts
// filterSQL emits — the Go-side twin of the SQL regex guard.
var numericLiteral = regexp.MustCompile(numericLiteralPattern)

// filterSQL renders one FilterCondition against the metadata map (or the
// type column), returning the fragment and its ordered args. base is the
// number of args already placed.
func filterSQL(f FilterCondition, base int) (string, []any) {
	var frag string
	var args []any
	switch {
	case f.Key == "type" && f.FilterType == "":
		frag = fmt.Sprintf("type = $%d", base+1)
		args = []any{f.Value}
	case f.FilterType == "numeric":
		// The operator is the ONE fragment that reaches SQL outside the
		// parameter protocol — whitelist it. Today's only caller is the
		// hardcoded FilterNumeric(">=") confidence floor, but any future
		// path routing a model- or user-derived operator here would be a
		// direct injection without this gate.
		// Fail closed on an unrecognized operator: silently substituting "="
		// turns a typo into a DIFFERENT filter that quietly returns the wrong
		// rows. Matching nothing is at least visibly wrong.
		var op string
		switch f.NumericOperator {
		case ">=", "<=", ">", "<", "=":
			op = f.NumericOperator
		case "":
			op = "="
		default:
			return "false", nil
		}
		// Guarded cast: a malformed value (e.g. a legacy backfilled doc with
		// confidence "high") must fail to MATCH, not error the whole leg with
		// 22P02 — Supermemory's semantics, and one bad _shared row must never
		// brick every confidence-floored briefing read.
		// Both sides must be guarded, not just the stored value: an
		// unparseable ARGUMENT would raise 22P02 and fail the whole leg (and
		// via the fan-out's single-error policy, the whole read) — the exact
		// failure this case exists to prevent.
		if !numericLiteral.MatchString(f.Value) {
			return "false", nil
		}
		frag = fmt.Sprintf(
			`CASE WHEN metadata->>$%d ~ '%s' THEN (metadata->>$%d)::numeric END %s $%d::numeric`,
			base+1, numericLiteralPattern, base+1, op, base+2)
		args = []any{f.Key, f.Value}
	case f.FilterType == "array_contains":
		// The PG store's metadata is the flat string map Metadata.ToMap
		// emits — array values never exist here, so an array-containment
		// filter matches nothing. Explicit FALSE beats the silent
		// fall-through to string equality, which would return wrong rows.
		// Returning early also keeps Negate from inverting it into
		// `NOT COALESCE(false,false)` = TRUE, which would match every row.
		return "false", nil
	case f.FilterType == "string_contains":
		// Escape LIKE metacharacters so the value is a literal substring, not
		// a pattern — an unescaped "%" would match every row.
		frag = fmt.Sprintf("metadata->>$%d ILIKE '%%' || $%d || '%%'", base+1, base+2)
		esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Value)
		args = []any{f.Key, esc}
	default:
		frag = fmt.Sprintf("metadata->>$%d = $%d", base+1, base+2)
		args = []any{f.Key, f.Value}
	}
	if f.Negate {
		frag = "NOT COALESCE(" + frag + ", false)"
	}
	return frag, args
}

// lenFirst reports the first vector's dimensionality for error messages.
func lenFirst(vecs [][]float32) int {
	if len(vecs) == 0 {
		return 0
	}
	return len(vecs[0])
}

// Search implements the reader seam over the hybrid SQL core; the shared
// orchestration owns container resolution, timeout, and fan-out merge, so
// retrieval requests are identical across backends by construction.
func (idx *PGIndexer) Search(ctx context.Context, q MemoryQuery) ([]PatternMatch, error) {
	return searchWith(ctx, idx.memoRunSearch(), q)
}

// Briefing implements the briefing seam over the same core: shared assembly
// (specialist block + review side-searches, shared floors and per-leg
// degradation) and the pure renderers.
func (idx *PGIndexer) Briefing(ctx context.Context, q BriefingQuery) (string, error) {
	return briefingWith(ctx, idx.memoRunSearch(), idx.logger, q)
}
