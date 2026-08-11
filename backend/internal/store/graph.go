package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// FileMemory holds all memory data for a specific file path.
type FileMemory struct {
	FilePath       string          `json:"file_path"`
	RiskScore      FileRisk        `json:"risk_score"`
	Patterns       []Pattern       `json:"patterns"`
	RecentComments []ReviewComment `json:"recent_comments"`
	Traces         []DecisionTrace `json:"traces"`
}

// installationOfRepo is the ONLY expression that writes code_nodes.installation_id.
//
// The column is a denormalisation of repos.installation_id, kept because pgGraph
// can only filter a traversal on a registered column of the node table. Deriving
// it in SQL rather than taking it as a parameter means the two can never
// disagree: a caller cannot stamp one tenant's id onto another tenant's node,
// and a repo whose installation_id ever changed self-heals on its next index
// pass because the ON CONFLICT branch rewrites the column too.
//
// It is spliced into the INSERT column list at the position of the repo_id
// parameter, so every writer must pass repo_id as $1.
const installationOfRepo = `(SELECT r.installation_id FROM repos r WHERE r.id = $1)`

// UpsertCodeNode inserts or updates a code node, returning its ID.
// Only updates base columns (kind, name, file_path, lines, language, pr_number).
// Does NOT overwrite type-info columns (return_type, params, etc.) if they already exist.
func (s *Store) UpsertCodeNode(ctx context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, language string, prNumber int) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO code_nodes (repo_id, installation_id, kind, name, file_path, line_start, line_end, language, pr_number, updated_at)
		VALUES ($1, `+installationOfRepo+`, $2, $3, $4, $5, $6, $7, NULLIF($8, 0), NOW())
		ON CONFLICT (repo_id, file_path, kind, name)
		DO UPDATE SET line_start = $5, line_end = $6, language = $7,
		             pr_number = COALESCE(NULLIF($8, 0), code_nodes.pr_number),
		             installation_id = EXCLUDED.installation_id,
		             updated_at = NOW()
		RETURNING id
	`, repoID, kind, name, filePath, lineStart, lineEnd, language, prNumber).Scan(&id)
	return id, err
}

// NodeHashRow carries the minimum a hash-gated diff needs: the primary key
// (for batched orphan DELETEs), the identity pair (kind, name) used as the
// diff key, and the stored content hash. Kept intentionally narrow so the
// per-file SELECT stays cheap even on large files.
type NodeHashRow struct {
	ID          int64
	Kind        string
	Name        string
	ContentHash string
}

// GetNodesHashesForFile returns one NodeHashRow per existing code_node for the
// given (repoID, filePath). Used by the indexer's diff pass to decide which
// symbols are unchanged (skip), changed (upsert), or gone (orphan sweep).
//
// ContentHash will be empty string for rows predating migration 043 — those
// get an unconditional upsert on the next index run, which backfills the hash.
func (s *Store) GetNodesHashesForFile(ctx context.Context, repoID int64, filePath string) ([]NodeHashRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, kind, name, COALESCE(content_hash, '')
		FROM code_nodes WHERE repo_id = $1 AND file_path = $2
	`, repoID, filePath)
	if err != nil {
		return nil, fmt.Errorf("get node hashes for file: %w", err)
	}
	defer rows.Close()
	return collectOrEmpty(rows, func(row pgx.CollectableRow) (NodeHashRow, error) {
		var n NodeHashRow
		err := row.Scan(&n.ID, &n.Kind, &n.Name, &n.ContentHash)
		return n, err
	})
}

// UpsertCodeNodeFullWithHash writes a code node with type info plus a
// content_hash. The hash is written on both INSERT and UPDATE paths so the next diff pass
// can compare against it. Calling code is expected to have already verified
// the hash does NOT match an existing row — skipping unchanged rows entirely
// is the whole point of the diff.
func (s *Store) UpsertCodeNodeFullWithHash(ctx context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, language string, prNumber int, returnType, params, visibility string, isAsync bool, receiverType, scope, contentHash string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO code_nodes (repo_id, installation_id, kind, name, file_path, line_start, line_end, language, pr_number, return_type, params, visibility, is_async, receiver_type, scope, content_hash, updated_at)
		VALUES ($1, `+installationOfRepo+`, $2, $3, $4, $5, $6, $7, NULLIF($8, 0), $9, $10, $11, $12, $13, $14, $15, NOW())
		ON CONFLICT (repo_id, file_path, kind, name)
		DO UPDATE SET line_start = $5, line_end = $6, language = $7,
		             pr_number = COALESCE(NULLIF($8, 0), code_nodes.pr_number),
		             return_type = $9, params = $10, visibility = $11,
		             is_async = $12, receiver_type = $13, scope = $14,
		             content_hash = $15,
		             installation_id = EXCLUDED.installation_id,
		             updated_at = NOW()
		RETURNING id
	`, repoID, kind, name, filePath, lineStart, lineEnd, language, prNumber, returnType, params, visibility, isAsync, receiverType, scope, contentHash).Scan(&id)
	return id, err
}

// DeleteNodesByIDs batches the orphan sweep at the end of an incremental
// index pass. The single DELETE replaces per-symbol deletes and typically
// hits 0 rows (symbols only sweep on rename/removal). Restricted to the
// given repoID so a stray ID from another repo can't cross-delete.
func (s *Store) DeleteNodesByIDs(ctx context.Context, repoID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM code_nodes WHERE repo_id = $1 AND id = ANY($2::bigint[])`,
		repoID, ids)
	return err
}

// UpsertCodeEdge inserts a code edge, ignoring duplicates.
//
// code_edges hash-gating is intentionally deferred. The ON CONFLICT DO
// NOTHING clause already makes re-upserts of unchanged edges a cheap
// no-op on the write path (no row rewrite, no WAL volume). Introducing a
// per-file edge diff mirrors the node-side machinery (see indexer.go
// planSymbolDiff) and has a real implementation cost — we only take that
// cost once Neon's Rows chart shows code_edges churn materially
// exceeding code_nodes. Until then the no-op on conflict is sufficient.
func (s *Store) UpsertCodeEdge(ctx context.Context, repoID, sourceID, targetID int64, kind string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO code_edges (repo_id, source_id, target_id, kind, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (repo_id, source_id, target_id, kind) DO NOTHING
	`, repoID, sourceID, targetID, kind)
	return err
}

// MarkNodesMerged marks all code_nodes for a given PR as permanently merged.
func (s *Store) MarkNodesMerged(ctx context.Context, repoID int64, prNumber int) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE code_nodes SET is_merged = true WHERE repo_id = $1 AND pr_number = $2`,
		repoID, prNumber)
	return err
}

// DeleteUnmergedNodesByPR deletes code_nodes added by a specific PR that haven't been merged.
func (s *Store) DeleteUnmergedNodesByPR(ctx context.Context, repoID int64, prNumber int) error {
	_, err := s.Pool.Exec(ctx,
		`DELETE FROM code_nodes WHERE repo_id = $1 AND pr_number = $2 AND is_merged = false`,
		repoID, prNumber)
	return err
}

// DeleteNodesByFile deletes all code nodes (and cascading edges) for a file.
func (s *Store) DeleteNodesByFile(ctx context.Context, repoID int64, filePath string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM code_nodes WHERE repo_id = $1 AND file_path = $2`, repoID, filePath)
	return err
}

// pgGraphOnce guards a single probe for a usable pgGraph projection.
// pgGraphReady is written after the probe and again if a traversal ever fails,
// so it is atomic rather than a plain bool.
var (
	pgGraphOnce  sync.Once
	pgGraphReady atomic.Bool
)

// pgGraphAvailable reports whether the graph extension is installed and its
// projection is usable. Probed once per process; any error answers false,
// which falls back to the recursive CTE.
//
// The columns are checked by name against graph.status() deliberately. An
// earlier version tested `WHERE built`, and graph.status() has no such column
// -- so the query errored on EVERY call, the probe answered false, and the
// pgGraph path never ran once in production. It failed to the CTE, so nothing
// looked broken; the feature was simply dead. Any predicate here must name a
// column that exists, because the failure is invisible.
//
// schema_status = 'current' means the projection matches the registered
// tables; read_only means it cannot serve.
//
// node_count is deliberately NOT checked, and this is the subtle part. pgGraph
// keeps its Engine in THREAD-LOCAL storage, one per PostgreSQL backend, with
// no shared Rust heap between connections; graph.status() reports that
// backend-local engine. A pooled connection that has not yet touched the graph
// therefore reports node_count = 0 while a traversal on the very same
// connection succeeds, because the query path auto-loads the persisted
// artifact on demand. Gating on node_count would disable pgGraph on precisely
// the cold connections that were about to load it -- nondeterministically,
// depending on which pool member served the probe.
//
// The fallback is not a nicety. pgGraph needs a custom build step and is absent
// from every managed Postgres, so a self-hosted install will never have it.
// Hard-requiring it would brick those deployments -- the same defect a
// mandatory CREATE EXTENSION introduced for pgcontext earlier.
func (s *Store) pgGraphAvailable(ctx context.Context) bool {
	pgGraphOnce.Do(func() {
		var ready bool
		if err := s.Pool.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'graph')
			   AND EXISTS (
			         SELECT 1 FROM graph.status()
			         WHERE schema_status = 'current' AND NOT read_only
			       )`).Scan(&ready); err != nil {
			return // stays false: the CTE needs no extension
		}
		pgGraphReady.Store(ready)
	})
	return pgGraphReady.Load()
}

// disablePGGraph turns the traversal path off for the rest of the process and
// says why, exactly once.
//
// A traversal error here is structural, not transient: the filter column is not
// registered (migration 072 raises a NOTICE and continues when
// graph.add_filter_column fails, and lib/pq discards NOTICEs, so the migration
// reports success either way), or the projection does not carry code_nodes.
// Without this the process re-issues one guaranteed-failing query per reviewed
// file per review, forever, and pgGraphAvailable cannot see it — the probe reads
// pg_extension and graph.status(), neither of which knows about filter columns.
// That is the same invisible failure as the `WHERE built` predicate described
// above, where the fast path was dead in production and nothing looked broken.
// The CTE is measured FASTER on this fleet, so turning the path off costs
// nothing and this log line is the only signal that it happened.
func disablePGGraph(err error) {
	if pgGraphReady.CompareAndSwap(true, false) {
		slog.Warn("pggraph blast radius failed; using the recursive CTE for the rest of this process", "error", err)
	}
}

// GetBlastRadius finds all nodes transitively depending on the given file paths
// up to maxDepth hops.
//
// Two implementations, selected by what the database actually has. pgGraph
// walks a CSR projection; the recursive CTE walks code_edges directly. They
// return the same shape.
//
// MEASURED, so the choice is not folklore: on this fleet (13.6k nodes, 13.4k
// edges) the CTE answers in 0.9ms and pgGraph in 9-11ms warm, because pgGraph
// allocates visited/depth/parent metadata proportional to the WHOLE graph per
// call while the CTE touches ~6 rows through an index. That inverts once the
// graph is large enough for the setup to amortise -- their own published
// figure is 107ms for a depth-2 walk over 2M nodes, where a CTE would be far
// worse.
//
// THE TENANT BOUNDARY IS installationID, NOT repoID, and the two are used for
// different jobs. repoID resolves the SEEDS: the changed paths came from one
// pull request in one repository, and two repositories in the same installation
// routinely contain the same path (`src/index.ts`), so seeding without it walks
// from the wrong file. installationID bounds the WALK: repositories inside one
// installation legitimately depend on each other (an API repo and the web repo
// that calls it), and scoping the walk to repoID makes those edges unreachable —
// which is what #221 item 2 exists to fix. Across installations there is no
// legitimate edge at all, so the walk must never leave the installation.
//
// Passing an installationID that does not own repoID yields no seeds and
// therefore an empty result. That is deliberate: a mismatched pair fails closed
// rather than resolving seeds in one tenant and walking in another.
func (s *Store) GetBlastRadius(ctx context.Context, installationID, repoID int64, filePaths []string, maxDepth int) ([]CodeNode, error) {
	if len(filePaths) == 0 {
		return []CodeNode{}, nil
	}
	if s.pgGraphAvailable(ctx) {
		// An EMPTY result falls through too, not just an error. A projection
		// that is stale or not yet built answers a traversal with zero rows
		// rather than failing, and an empty blast radius is indistinguishable
		// from "nothing depends on this file" at every call site. Re-running
		// the CTE costs ~1ms and is the only way to tell the two apart.
		nodes, err := s.blastRadiusPGGraph(ctx, installationID, repoID, filePaths, maxDepth)
		if err != nil {
			disablePGGraph(err)
		} else if len(nodes) > 0 {
			return nodes, nil
		}
	}
	return s.blastRadiusCTE(ctx, installationID, repoID, filePaths, maxDepth)
}

// blastRadiusPGGraph resolves the seed file paths to node ids, then multi-start
// traverses the projection. Direction "in" walks edges backwards -- from a
// changed node to the nodes that DEPEND on it, which is what blast radius
// means. Seeds are resolved from code_nodes rather than graph.search() because
// the source table is authoritative and already indexed on (repo_id, file_path).
//
// installation_id is pushed INTO the traversal as a registered filter column,
// not applied to its output. max_rows is enforced inside traverse, so a
// post-filter lets another tenant's nodes consume the budget and then be
// discarded -- returning fewer rows than the CTE, or none. code_edges is
// registered with no tenant boundary of its own, so an unfiltered walk can reach
// any node in the database; the node-side filter is the only thing stopping it.
// The trailing WHERE on the hydration join is defence in depth, not the
// boundary: it runs after max_rows and cannot recover rows already spent.
//
// The inner max_rows (200) is deliberately wider than the 50 rows returned.
// Since the walk crosses repositories, a sibling repository can now supply more
// depth-1 dependents than the whole budget, and traverse spends its budget
// DURING the walk with no repo awareness — so a budget equal to the result size
// would let one repository fill it and leave nothing for the repository the pull
// request is actually in. Ordering same-repo first (see the CTE) can only choose
// among rows the walk returned, which is why the walk has to return more.
func (s *Store) blastRadiusPGGraph(ctx context.Context, installationID, repoID int64, filePaths []string, maxDepth int) ([]CodeNode, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH seeds AS (
		  SELECT array_agg(id::text) AS ids
		  FROM code_nodes WHERE installation_id = $1 AND repo_id = $2 AND file_path = ANY($3)
		), reached AS (
		  SELECT DISTINCT cn.id, cn.repo_id, cn.name, cn.file_path, cn.kind, t.depth
		  FROM seeds s
		  CROSS JOIN LATERAL graph.traverse(
		      (SELECT array_agg('public.code_nodes'::regclass) FROM generate_series(1, array_length(s.ids, 1))),
		      s.ids,
		      max_depth := $4,
		      direction := 'in',
		      hydrate := false,
		      filter := graph.eq('installation_id', to_jsonb($1::bigint)),
		      max_rows := 200
		  ) t
		  JOIN code_nodes cn ON cn.id = t.node_id::bigint
		  WHERE cn.installation_id = $1
		)
		SELECT id, repo_id, name, file_path, kind, depth FROM reached
		ORDER BY depth, (repo_id <> $2), file_path
		LIMIT 50`, installationID, repoID, filePaths, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("pggraph blast radius: %w", err)
	}
	defer rows.Close()
	return collectOrEmpty(rows, func(row pgx.CollectableRow) (CodeNode, error) {
		var n CodeNode
		err := row.Scan(&n.ID, &n.RepoID, &n.Name, &n.FilePath, &n.Kind, &n.Depth)
		return n, err
	})
}

// blastRadiusCTE is the fallback, and it must return the SAME SET as
// blastRadiusPGGraph or the two paths disagree on who a customer's dependents
// are depending on which extensions the database happens to have. Its recursive
// step therefore filters on installation_id — the same boundary the traversal
// filter enforces — and NOT on repo_id, which would silently stop the walk at
// the repository edge and hide every cross-repo dependent.
//
// repo_id is SELECTED, not just filtered on, because the walk now returns nodes
// from sibling repositories and the caller has to be able to tell which. Both
// consumers resolve a dependent's path against the pull request's own
// repository at its head SHA; handed a sibling repository's path with no way to
// recognise it, they either fetch a same-named file from the wrong repository or
// silently drop the dependent.
//
// `AND NOT ce.inferred` is load-bearing since the walk became installation-
// scoped. While the recursion filtered repo_id, a derived cross-repo calls_api
// edge could not be traversed: its target node sat in another repository and
// the predicate stopped there. Widening the walk to the installation removed
// exactly that accident, so the derived edge is now reachable and an inferred
// relation would enter review context as though a parser had found it. Nothing
// consumes these edges deliberately yet, so they are excluded here rather than
// silently promoted to fact. It is the same predicate ListGraphEdges,
// ListArchFileEdges, GetTopChokePoints and GetFileFanIn already carry.
//
// KNOWN DIVERGENCE: the pgGraph path above cannot carry this. pgGraph filters
// are evaluated against registered columns of the NODE table, and `inferred` is
// a column of code_edges — migration 072 records that a join cannot be pushed
// into graph.traverse(). Where the extension is built and the projection is
// fresh, a derived edge is therefore still walked. Consuming these edges on
// purpose (#221 item 3b) has to settle that before the projection can be
// trusted as equivalent to the CTE.
//
// The result is ordered same-repo-first WITHIN each depth. The row budget did
// not grow when the walk widened from one repository to a whole installation, so
// ordering on bare file_path let an alphabetically earlier sibling repository
// (`admin-ui/...` before `api/...`) consume all 50 slots and evict the
// dependents the pre-#221 query was guaranteed to return — a regression in the
// case that already worked, with no signal that truncation happened. Depth stays
// the primary key because both consumers filter on depth == 1.
func (s *Store) blastRadiusCTE(ctx context.Context, installationID, repoID int64, filePaths []string, maxDepth int) ([]CodeNode, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH RECURSIVE affected AS (
			SELECT id, repo_id, name, file_path, kind, 0 as depth
			FROM code_nodes WHERE installation_id = $1 AND repo_id = $2 AND file_path = ANY($3)
			UNION
			SELECT cn.id, cn.repo_id, cn.name, cn.file_path, cn.kind, a.depth + 1
			FROM code_nodes cn
			JOIN code_edges ce ON ce.source_id = cn.id
			JOIN affected a ON ce.target_id = a.id
			WHERE a.depth < $4 AND cn.installation_id = $1 AND NOT ce.inferred
		)
		SELECT id, repo_id, name, file_path, kind, depth
		FROM (SELECT DISTINCT id, repo_id, name, file_path, kind, depth FROM affected) d
		ORDER BY depth, (repo_id <> $2), file_path
		LIMIT 50
	`, installationID, repoID, filePaths, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("blast radius query: %w", err)
	}
	defer rows.Close()
	return collectOrEmpty(rows, func(row pgx.CollectableRow) (CodeNode, error) {
		var n CodeNode
		err := row.Scan(&n.ID, &n.RepoID, &n.Name, &n.FilePath, &n.Kind, &n.Depth)
		return n, err
	})
}

// GetCodeNodesForFile returns all code nodes for a given file with full type info, ordered by line_start.
func (s *Store) GetCodeNodesForFile(ctx context.Context, repoID int64, filePath string) ([]CodeNode, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, repo_id, kind, name, file_path,
		       COALESCE(line_start, 0), COALESCE(line_end, 0), COALESCE(language, ''),
		       COALESCE(return_type, ''), COALESCE(params, ''), COALESCE(visibility, ''),
		       COALESCE(is_async, false), COALESCE(receiver_type, ''), COALESCE(scope, '')
		FROM code_nodes WHERE repo_id = $1 AND file_path = $2
		ORDER BY line_start
	`, repoID, filePath)
	if err != nil {
		return nil, fmt.Errorf("get code nodes for file: %w", err)
	}
	defer rows.Close()
	return collectOrEmpty(rows, func(row pgx.CollectableRow) (CodeNode, error) {
		var n CodeNode
		err := row.Scan(&n.ID, &n.RepoID, &n.Kind, &n.Name, &n.FilePath,
			&n.LineStart, &n.LineEnd, &n.Language,
			&n.ReturnType, &n.Params, &n.Visibility,
			&n.IsAsync, &n.ReceiverType, &n.Scope)
		return n, err
	})
}

// GetFileMemory returns patterns, review comments, decision traces, and risk score for a file.
func (s *Store) GetFileMemory(ctx context.Context, repoID int64, filePath string) (*FileMemory, error) {
	mem := &FileMemory{
		FilePath:       filePath,
		Patterns:       []Pattern{},
		RecentComments: []ReviewComment{},
		Traces:         []DecisionTrace{},
	}

	// Risk score
	var lastTrace time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*)::int, COALESCE(MAX(created_at), '0001-01-01')
		FROM decision_traces WHERE repo_id = $1 AND file_path = $2
	`, repoID, filePath).Scan(&mem.RiskScore.TraceCount, &lastTrace)
	if err != nil {
		return nil, fmt.Errorf("file memory risk: %w", err)
	}
	mem.RiskScore.FilePath = filePath
	mem.RiskScore.LastTrace = lastTrace

	// Patterns linked via review comments on this file
	pRows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT p.id, p.installation_id, p.repo_id, p.content, p.memory_doc_id,
		       p.created_by, COALESCE(p.source, 'manual'), p.category, p.pr_number, p.created_at, p.updated_at
		FROM patterns p
		JOIN review_comments rc ON rc.matched_pattern_id = p.id
		JOIN reviews r ON r.id = rc.review_id
		WHERE rc.file_path = $1 AND r.repo_id = $2
		  AND rc.attempt_generation = r.attempt_generation
		ORDER BY p.created_at DESC LIMIT 10
	`, filePath, repoID)
	if err != nil {
		return nil, fmt.Errorf("file memory patterns: %w", err)
	}
	defer pRows.Close()
	mem.Patterns, err = collectOrEmpty(pRows, pgx.RowToStructByPos[Pattern])
	if err != nil {
		return nil, fmt.Errorf("file memory patterns scan: %w", err)
	}

	// Recent comments on this file. Suppressed findings stay in the payload —
	// the sidebar shows them as an audit record — but sort AFTER posted ones:
	// a burst of suppressions on one file would otherwise fill the whole LIMIT
	// window and hide every finding Argus actually posted on that file.
	// state is NOT NULL DEFAULT 'posted' (migration 051), so the boolean key is
	// never NULL and false (posted) always sorts first.
	cRows, err := s.Pool.Query(ctx, `
		SELECT rc.id, rc.review_id, rc.file_path, rc.start_line, rc.end_line, rc.side,
		       rc.body, rc.severity, rc.category, rc.specialist, rc.confidence_score,
		       rc.code_snippet, rc.github_comment_id, rc.matched_pattern_id,
		       rc.matched_pattern_score, rc.enforced_rule_content, rc.is_new_finding, rc.created_at,
		       rc.state, rc.suppressed_reason, rc.resolved_sha, rc.attempt_generation
		FROM review_comments rc
		JOIN reviews r ON r.id = rc.review_id
		WHERE rc.file_path = $1 AND r.repo_id = $2
		  AND rc.attempt_generation = r.attempt_generation
		ORDER BY (rc.state = 'suppressed'), rc.created_at DESC LIMIT 5
	`, filePath, repoID)
	if err != nil {
		return nil, fmt.Errorf("file memory comments: %w", err)
	}
	defer cRows.Close()
	mem.RecentComments, err = collectOrEmpty(cRows, pgx.RowToStructByPos[ReviewComment])
	if err != nil {
		return nil, fmt.Errorf("file memory comments scan: %w", err)
	}

	// Decision traces
	tRows, err := s.Pool.Query(ctx, `
		SELECT id, repo_id, file_path, COALESCE(symbol_name, ''), trace_type, content,
		       COALESCE(severity, ''), review_id, COALESCE(pr_number, 0), COALESCE(metadata, '{}'), created_at
		FROM decision_traces WHERE repo_id = $1 AND file_path = $2
		ORDER BY created_at DESC LIMIT 10
	`, repoID, filePath)
	if err != nil {
		return nil, fmt.Errorf("file memory traces: %w", err)
	}
	defer tRows.Close()
	mem.Traces, err = collectOrEmpty(tRows, func(row pgx.CollectableRow) (DecisionTrace, error) {
		var t DecisionTrace
		var metaJSON []byte
		err := row.Scan(&t.ID, &t.RepoID, &t.FilePath, &t.SymbolName, &t.TraceType, &t.Content,
			&t.Severity, &t.ReviewID, &t.PRNumber, &metaJSON, &t.CreatedAt)
		if err != nil {
			return t, err
		}
		if len(metaJSON) > 0 {
			_ = json.Unmarshal(metaJSON, &t.Metadata)
		}
		return t, nil
	})
	if err != nil {
		return nil, fmt.Errorf("file memory traces scan: %w", err)
	}

	return mem, nil
}
