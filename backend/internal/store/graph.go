package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
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

// publishedGraphReposCTE is the shared publication-authority predicate for raw
// semantic graph reads. Physical code_nodes/code_edges rows are deliberately
// retained across migrations and failed refreshes, so their existence alone is
// never authority. The generation must be the owning repo's published pointer,
// belong to that same repo, and still be marked published.
//
// Writer/staging queries must not use this CTE: they need to inspect and mutate
// physical rows before the first publication and while a later generation is
// building.
const publishedGraphReposCTE = `authoritative_graph_repos AS (
	SELECT authority.id
	FROM repos authority
	JOIN graph_index_generations published
	  ON published.id = authority.graph_published_generation_id
	 AND published.repo_id = authority.id
	 AND published.status = 'published'
)`

// UpsertCodeNode inserts or updates a code node, returning its ID.
// Only updates base columns (kind, name, file_path, lines, language, pr_number).
// Does NOT overwrite type-info columns (return_type, params, etc.) if they already exist.
func (s *Store) UpsertCodeNode(ctx context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, language string, prNumber int) (int64, error) {
	return s.q.UpsertCodeNode(ctx, db.UpsertCodeNodeParams{RepoID: repoID, Kind: kind, Name: name, FilePath: filePath, LineStart: &lineStart, LineEnd: &lineEnd, Language: &language, PRNumber: &prNumber})
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
	return s.q.UpsertCodeEdge(ctx, db.UpsertCodeEdgeParams{RepoID: repoID, SourceID: sourceID, TargetID: targetID, Kind: kind})
}

// CodeEdgeRow is one parser-owned edge in an authoritative per-file snapshot.
type CodeEdgeRow struct {
	SourceID int64
	TargetID int64
	Kind     string
}

// ReplaceCodeEdgesForFiles replaces every deterministic outgoing edge whose
// source belongs to one of filePaths. An empty edge set is authoritative and
// removes relationships that disappeared from the source.
func (s *Store) ReplaceCodeEdgesForFiles(ctx context.Context, repoID int64, filePaths []string, edges []CodeEdgeRow) error {
	if len(filePaths) == 0 {
		return nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("replace code edges: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		DELETE FROM code_edges ce
		USING code_nodes src
		WHERE ce.source_id = src.id
		  AND ce.repo_id = $1 AND src.repo_id = $1
		  AND src.file_path = ANY($2::text[])
		  AND NOT ce.inferred`, repoID, filePaths); err != nil {
		return fmt.Errorf("replace code edges: delete: %w", err)
	}
	if len(edges) > 0 {
		batch := &pgx.Batch{}
		for _, edge := range edges {
			batch.Queue(`
				INSERT INTO code_edges (repo_id, source_id, target_id, kind, inferred, updated_at)
				VALUES ($1, $2, $3, $4, false, NOW())
				ON CONFLICT (repo_id, source_id, target_id, kind) DO NOTHING`,
				repoID, edge.SourceID, edge.TargetID, edge.Kind)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return fmt.Errorf("replace code edges: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("replace code edges: commit: %w", err)
	}
	return nil
}

// MarkNodesMerged marks all code_nodes for a given PR as permanently merged.
func (s *Store) MarkNodesMerged(ctx context.Context, repoID int64, prNumber int) error {
	return s.q.MarkNodesMerged(ctx, db.MarkNodesMergedParams{RepoID: repoID, PRNumber: &prNumber})
}

// DeleteUnmergedNodesByPR deletes code_nodes added by a specific PR that haven't been merged.
func (s *Store) DeleteUnmergedNodesByPR(ctx context.Context, repoID int64, prNumber int) error {
	return s.q.DeleteUnmergedNodesByPR(ctx, db.DeleteUnmergedNodesByPRParams{RepoID: repoID, PRNumber: &prNumber})
}

// DeleteNodesByFile deletes all code nodes (and cascading edges) for a file.
func (s *Store) DeleteNodesByFile(ctx context.Context, repoID int64, filePath string) error {
	return s.q.DeleteNodesByFile(ctx, db.DeleteNodesByFileParams{RepoID: repoID, FilePath: filePath})
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

// blastRadiusPGGraph exercises the installed projection, then validates its
// candidate walk against authoritative code_edges before returning rows.
//
// pgGraph can filter node columns (including installation_id), but it cannot
// filter code_edges.inferred. Returning graph.traverse output directly would
// therefore promote derived API guesses into blast-radius facts. Post-filtering
// reached nodes is also insufficient: an inferred edge may lead to a node that
// has a separate parsed path, and an inferred-edge flood can consume max_rows
// before parsed dependents are returned.
//
// The candidates CTE deliberately remains in the query and projection_state
// forces its evaluation, so a missing/stale/unusable projection still fails and
// GetBlastRadius disables the fast path. The reached CTE is the policy gate: it
// walks only NOT inferred edges under the same installation boundary and row
// ordering as blastRadiusCTE. This trades some speed for engine-independent
// answers until pgGraph supports edge predicates.
//
// The projection probe is bounded at 200 candidates. That budget cannot affect
// the returned set because candidates are never used as an inclusion filter.
func (s *Store) blastRadiusPGGraph(ctx context.Context, installationID, repoID int64, filePaths []string, maxDepth int) ([]CodeNode, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH RECURSIVE `+publishedGraphReposCTE+`, seeds AS (
		  SELECT cn.id, cn.repo_id, cn.name, cn.file_path, cn.kind
		  FROM code_nodes cn
		  JOIN authoritative_graph_repos authority ON authority.id = cn.repo_id
		  WHERE cn.installation_id = $1 AND cn.repo_id = $2 AND cn.file_path = ANY($3)
		), candidates AS (
		  SELECT DISTINCT t.node_id::bigint AS id
		  FROM (SELECT array_agg(id::text) AS ids FROM seeds) s
		  CROSS JOIN LATERAL graph.traverse(
		      (SELECT array_agg('public.code_nodes'::regclass) FROM generate_series(1, array_length(s.ids, 1))),
		      s.ids,
		      max_depth := $4,
		      direction := 'in',
		      hydrate := false,
		      filter := graph.eq('installation_id', to_jsonb($1::bigint)),
		      max_rows := 200
		  ) t
		), reached AS (
		  SELECT id, repo_id, name, file_path, kind, 0 AS depth FROM seeds
		  UNION
		  SELECT cn.id, cn.repo_id, cn.name, cn.file_path, cn.kind, r.depth + 1
		  FROM reached r
		  JOIN code_edges ce ON ce.target_id = r.id AND NOT ce.inferred
		  JOIN code_nodes cn ON cn.id = ce.source_id AND cn.repo_id = ce.repo_id
		  JOIN authoritative_graph_repos authority ON authority.id = cn.repo_id
		  WHERE r.depth < $4 AND cn.installation_id = $1
		), projection_state AS (
		  SELECT COUNT(*) AS candidate_count FROM candidates
		)
		SELECT d.id, d.repo_id, d.name, d.file_path, d.kind, d.depth
		FROM (SELECT DISTINCT id, repo_id, name, file_path, kind, depth FROM reached) d
		CROSS JOIN projection_state p
		WHERE p.candidate_count >= 0
		ORDER BY d.depth, (d.repo_id <> $2), d.file_path
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
// The pgGraph query carries this same recursive policy gate after its bounded
// projection probe. An inferred edge therefore cannot change the answer merely
// because the optional extension is installed.
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
		WITH RECURSIVE `+publishedGraphReposCTE+`, affected AS (
			SELECT cn.id, cn.repo_id, cn.name, cn.file_path, cn.kind, 0 as depth
			FROM code_nodes cn
			JOIN authoritative_graph_repos authority ON authority.id = cn.repo_id
			WHERE cn.installation_id = $1 AND cn.repo_id = $2 AND cn.file_path = ANY($3)
			UNION
			SELECT cn.id, cn.repo_id, cn.name, cn.file_path, cn.kind, a.depth + 1
			FROM code_nodes cn
			JOIN code_edges ce ON ce.source_id = cn.id AND ce.repo_id = cn.repo_id
			JOIN affected a ON ce.target_id = a.id
			JOIN authoritative_graph_repos authority ON authority.id = cn.repo_id
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
		WITH `+publishedGraphReposCTE+`
		SELECT cn.id, cn.repo_id, cn.kind, cn.name, cn.file_path,
		       COALESCE(cn.line_start, 0), COALESCE(cn.line_end, 0), COALESCE(cn.language, ''),
		       COALESCE(cn.return_type, ''), COALESCE(cn.params, ''), COALESCE(cn.visibility, ''),
		       COALESCE(cn.is_async, false), COALESCE(cn.receiver_type, ''), COALESCE(cn.scope, '')
		FROM code_nodes cn
		JOIN authoritative_graph_repos authority ON authority.id = cn.repo_id
		WHERE cn.repo_id = $1 AND cn.file_path = $2
		ORDER BY cn.line_start
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

	// Patterns linked via review comments on this file.
	patternRows, err := s.q.GetFileMemoryPatterns(ctx, db.GetFileMemoryPatternsParams{FilePath: filePath, RepoID: repoID})
	if err != nil {
		return nil, fmt.Errorf("file memory patterns: %w", err)
	}
	for _, row := range patternRows {
		pattern, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("file memory patterns: %w", err)
		}
		mem.Patterns = append(mem.Patterns, pattern)
	}

	// Recent comments on this file. Suppressed findings stay in the payload —
	// the sidebar shows them as an audit record — but sort AFTER posted ones:
	// a burst of suppressions on one file would otherwise fill the whole LIMIT
	// window and hide every finding Argus actually posted on that file.
	// state is NOT NULL DEFAULT 'posted' (migration 051), so the boolean key is
	// never NULL and false (posted) always sorts first.
	commentRows, err := s.q.GetFileMemoryComments(ctx, db.GetFileMemoryCommentsParams{FilePath: filePath, RepoID: repoID})
	if err != nil {
		return nil, fmt.Errorf("file memory comments: %w", err)
	}
	for _, row := range commentRows {
		comment, err := fileMemoryCommentFromSQLC(row)
		if err != nil {
			return nil, fmt.Errorf("file memory comments: %w", err)
		}
		mem.RecentComments = append(mem.RecentComments, comment)
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
