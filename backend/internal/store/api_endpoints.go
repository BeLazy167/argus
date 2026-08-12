package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// APIEndpointRow is one persisted route declaration or call site. It is the
// storage form of graph.APIEndpoint; the graph package owns extraction and
// matching, this package owns persistence and the tenant boundary.
type APIEndpointRow struct {
	RepoID      int64
	NodeID      int64
	Role        string
	Method      string
	PathPattern string
	RawPath     string
	FilePath    string
	Line        int
}

// InferredEdgeRow is one DERIVED code_edges row. Separate from the parsed
// writer's arguments so an inferred edge can never be produced by a caller that
// thought it was writing a fact.
type InferredEdgeRow struct {
	RepoID   int64
	SourceID int64
	TargetID int64
	Kind     string
}

// MaxInstallationAPIEndpoints bounds one matching pass.
//
// The matcher loads an installation's endpoints into memory, so this is the
// only thing standing between a pathological repository — a generated client
// with thousands of call sites — and a backend that allocates without limit
// during a background index. Ordered by id so the window is deterministic
// rather than whatever the planner returns.
//
// Exported because the caller MUST be able to tell a full read from a clipped
// one: the rewrite path below assigns fresh ids to whichever repo was just
// indexed, which puts its rows at the END of `ORDER BY e.id`, so the rows this
// limit drops are exactly the freshest ones. A derived edge set computed from a
// clipped window would delete links it cannot re-derive.
const MaxInstallationAPIEndpoints = 20000

// endpointKey is the identity used to decide whether a file's endpoint set
// changed. It covers every column the row carries, so "unchanged" means
// byte-identical and the gate can never skip a real write.
func endpointKey(r APIEndpointRow) string {
	return strings.Join([]string{
		r.FilePath, r.Role, r.Method, r.PathPattern, r.RawPath,
		fmt.Sprint(r.Line), fmt.Sprint(r.NodeID),
	}, "\x1f")
}

func sameEndpointSet(a, b []APIEndpointRow) bool {
	if len(a) != len(b) {
		return false
	}
	ka := make([]string, 0, len(a))
	kb := make([]string, 0, len(b))
	for _, r := range a {
		ka = append(ka, endpointKey(r))
	}
	for _, r := range b {
		kb = append(kb, endpointKey(r))
	}
	sort.Strings(ka)
	sort.Strings(kb)
	for i := range ka {
		if ka[i] != kb[i] {
			return false
		}
	}
	return true
}

// ReplaceAPIEndpointsForFiles rewrites the endpoints of every file one index
// run visited, and reports whether the table actually changed.
//
// Delete-then-insert, not upsert, because the deletion is the point: a route
// that was removed from a file must leave the table. An upsert-only path would
// leave the old row behind and keep inferring an edge to an endpoint that no
// longer exists — an inferred edge that is not merely uncertain but wrong.
//
// One transaction and one statement per phase for the WHOLE run, not per file.
// Most source files in any repository declare no endpoint at all, and the
// per-file version spent three round trips (BEGIN / DELETE / COMMIT) on each of
// them — ~900 for a 300-file full-index window, inside the same 10s budget the
// incremental path shares with GitHub fetches.
//
// The equality gate mirrors the node path's hash gating (GetNodesHashesForFile
// + planSymbolDiff): a PR that touches twenty files declaring nothing must not
// rewrite rows, and the `false` it returns is what stops the caller re-deriving
// the whole installation's edge set for no reason.
func (s *Store) ReplaceAPIEndpointsForFiles(ctx context.Context, repoID int64, filePaths []string, rows []APIEndpointRow) (bool, error) {
	if len(filePaths) == 0 {
		return false, nil
	}

	existing, err := s.listAPIEndpointsForFiles(ctx, repoID, filePaths)
	if err != nil {
		return false, err
	}
	if sameEndpointSet(existing, rows) {
		return false, nil
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("replace api endpoints: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`DELETE FROM api_endpoints WHERE repo_id = $1 AND file_path = ANY($2::text[])`,
		repoID, filePaths); err != nil {
		return false, fmt.Errorf("replace api endpoints: delete: %w", err)
	}
	if len(rows) > 0 {
		batch := &pgx.Batch{}
		for _, r := range rows {
			batch.Queue(`
				INSERT INTO api_endpoints (repo_id, node_id, role, method, path_pattern, raw_path, file_path, line, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
				ON CONFLICT (repo_id, role, method, path_pattern, file_path, line) DO NOTHING`,
				repoID, r.NodeID, r.Role, r.Method, r.PathPattern, r.RawPath, r.FilePath, r.Line)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return false, fmt.Errorf("replace api endpoints: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("replace api endpoints: commit: %w", err)
	}
	return true, nil
}

func (s *Store) listAPIEndpointsForFiles(ctx context.Context, repoID int64, filePaths []string) ([]APIEndpointRow, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT repo_id, node_id, role, method, path_pattern, raw_path, file_path, line
		FROM api_endpoints
		WHERE repo_id = $1 AND file_path = ANY($2::text[])`, repoID, filePaths)
	if err != nil {
		return nil, fmt.Errorf("list api endpoints for files: %w", err)
	}
	defer rows.Close()
	return collectOrEmpty(rows, func(row pgx.CollectableRow) (APIEndpointRow, error) {
		var e APIEndpointRow
		err := row.Scan(&e.RepoID, &e.NodeID, &e.Role, &e.Method, &e.PathPattern, &e.RawPath, &e.FilePath, &e.Line)
		return e, err
	})
}

// ListAPIEndpointsForInstallationOf returns every endpoint belonging to the
// installation that owns repoID, including repoID's own.
//
// THE TENANT BOUNDARY LIVES HERE, in one predicate. Repos inside an
// installation may link to each other; repos in different installations must
// never — the same boundary the pattern_stats UNIQUE fix closed. The join goes
// through repos rather than reading a denormalised installation_id off
// api_endpoints, so there is no second copy of the key to drift out of sync.
//
// Reads one row past MaxInstallationAPIEndpoints so `truncated` can say whether
// the window was clipped. A silent clip is indistinguishable from "this
// installation has no cross-repo API calls", and the caller must be able to
// refuse to rewrite the derived edge set from a partial view.
func (s *Store) ListAPIEndpointsForInstallationOf(ctx context.Context, repoID int64) (endpoints []APIEndpointRow, truncated bool, err error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.repo_id, e.node_id, e.role, e.method, e.path_pattern, e.raw_path, e.file_path, e.line
		FROM api_endpoints e
		JOIN repos r ON r.id = e.repo_id
		WHERE r.installation_id = (SELECT installation_id FROM repos WHERE id = $1)
		ORDER BY e.id
		LIMIT $2`, repoID, MaxInstallationAPIEndpoints+1)
	if err != nil {
		return nil, false, fmt.Errorf("list api endpoints: %w", err)
	}
	defer rows.Close()
	out, err := collectOrEmpty(rows, func(row pgx.CollectableRow) (APIEndpointRow, error) {
		var e APIEndpointRow
		err := row.Scan(&e.RepoID, &e.NodeID, &e.Role, &e.Method, &e.PathPattern, &e.RawPath, &e.FilePath, &e.Line)
		return e, err
	})
	if err != nil {
		return nil, false, err
	}
	if len(out) > MaxInstallationAPIEndpoints {
		return out[:MaxInstallationAPIEndpoints], true, nil
	}
	return out, false, nil
}

// ReplaceInferredAPIEdges makes the derived calls_api edge set of ONE
// installation exactly `edges`, and returns how many rows it wrote.
//
// Replace, not upsert, because nothing else ever deletes these rows. The FK
// cascade from code_nodes only fires when a node disappears, and the endpoint
// that justified the edge can vanish while both nodes survive: rename a route
// and keep its handler, or delete the fetch() line and keep the hook around it.
// An upsert-only writer left that edge in place for ever — a claim that one
// function calls an API it does not call, indistinguishable from a live one,
// with no error, no log and no expiry. ReplaceAPIEndpointsForFiles deletes the
// endpoint row for exactly this reason; deleting the endpoint but not the edge
// it produced only moves the stale claim one table over.
//
// The whole installation is the unit because the caller re-derives the whole
// installation: the matcher reads every repo's endpoints, so a route change in
// one repo changes the edges owned by the repos that call it.
//
// ON CONFLICT DO NOTHING, matching UpsertCodeEdge. Its comment states the
// reason — a re-upsert of an unchanged edge must be a cheap no-op with no row
// rewrite and no WAL volume, which is why code_edges hash-gating was deferred.
func (s *Store) ReplaceInferredAPIEdges(ctx context.Context, repoID int64, edges []InferredEdgeRow) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("replace inferred api edges: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		DELETE FROM code_edges
		WHERE inferred AND kind = 'calls_api'
		  AND repo_id IN (
		      SELECT id FROM repos
		      WHERE installation_id = (SELECT installation_id FROM repos WHERE id = $1))`,
		repoID); err != nil {
		return 0, fmt.Errorf("replace inferred api edges: delete: %w", err)
	}

	if len(edges) > 0 {
		batch := &pgx.Batch{}
		for _, e := range edges {
			batch.Queue(`
				INSERT INTO code_edges (repo_id, source_id, target_id, kind, inferred, updated_at)
				VALUES ($1, $2, $3, $4, true, NOW())
				ON CONFLICT (repo_id, source_id, target_id, kind) DO NOTHING`,
				e.RepoID, e.SourceID, e.TargetID, e.Kind)
		}
		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			return 0, fmt.Errorf("replace inferred api edges: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("replace inferred api edges: commit: %w", err)
	}
	return len(edges), nil
}

// LookupCodeNodeIDsByName resolves symbol names for the given repository.
// Qualified names match exactly. An unqualified name also matches the final
// component of a receiver/scoped identity (Handle -> Alpha.Handle). A zero ID
// means the name exists but is ambiguous; callers must not choose one row.
func (s *Store) LookupCodeNodeIDsByName(ctx context.Context, repoID int64, names []string) (map[string]int64, error) {
	if len(names) == 0 {
		return map[string]int64{}, nil
	}
	rows, err := s.Pool.Query(ctx, `
		WITH requested(name, qualified) AS (
			SELECT name, strpos(name, '.') > 0 OR strpos(name, '::') > 0
			FROM unnest($2::text[]) name
		), matches AS (
			SELECT requested.name AS requested_name, cn.id
			FROM requested
			JOIN code_nodes cn ON cn.repo_id = $1 AND (
				cn.name = requested.name OR (NOT requested.qualified AND (
					right(cn.name, length(requested.name) + 1) = '.' || requested.name OR
					right(cn.name, length(requested.name) + 2) = '::' || requested.name
				)))
		)
		SELECT requested_name, CASE WHEN COUNT(*) = 1 THEN MIN(id) ELSE 0 END AS id
		FROM matches
		GROUP BY requested_name
		ORDER BY requested_name`, repoID, names)
	if err != nil {
		return nil, fmt.Errorf("lookup code node ids: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int64, len(names))
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			return nil, fmt.Errorf("lookup code node ids: scan: %w", err)
		}
		out[name] = id
	}
	return out, rows.Err()
}
