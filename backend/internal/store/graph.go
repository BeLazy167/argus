package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// FileMemory holds all memory data for a specific file path.
type FileMemory struct {
	FilePath       string           `json:"file_path"`
	RiskScore      FileRisk         `json:"risk_score"`
	Patterns       []Pattern        `json:"patterns"`
	RecentComments []ReviewComment  `json:"recent_comments"`
	Traces         []DecisionTrace  `json:"traces"`
}

// UpsertCodeNode inserts or updates a code node, returning its ID.
// Only updates base columns (kind, name, file_path, lines, language, pr_number).
// Does NOT overwrite type-info columns (return_type, params, etc.) if they already exist.
func (s *Store) UpsertCodeNode(ctx context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, language string, prNumber int) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language, pr_number, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, 0), NOW())
		ON CONFLICT (repo_id, file_path, kind, name)
		DO UPDATE SET line_start = $5, line_end = $6, language = $7,
		             pr_number = COALESCE(NULLIF($8, 0), code_nodes.pr_number),
		             updated_at = NOW()
		RETURNING id
	`, repoID, kind, name, filePath, lineStart, lineEnd, language, prNumber).Scan(&id)
	return id, err
}

// UpsertCodeNodeFull inserts or updates a code node with type info, returning its ID.
func (s *Store) UpsertCodeNodeFull(ctx context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, language string, prNumber int, returnType, params, visibility string, isAsync bool, receiverType, scope string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language, pr_number, return_type, params, visibility, is_async, receiver_type, scope, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, 0), $9, $10, $11, $12, $13, $14, NOW())
		ON CONFLICT (repo_id, file_path, kind, name)
		DO UPDATE SET line_start = $5, line_end = $6, language = $7,
		             pr_number = COALESCE(NULLIF($8, 0), code_nodes.pr_number),
		             return_type = $9, params = $10, visibility = $11,
		             is_async = $12, receiver_type = $13, scope = $14,
		             updated_at = NOW()
		RETURNING id
	`, repoID, kind, name, filePath, lineStart, lineEnd, language, prNumber, returnType, params, visibility, isAsync, receiverType, scope).Scan(&id)
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

// UpsertCodeNodeFullWithHash is UpsertCodeNodeFull plus a content_hash write.
// The hash is written on both INSERT and UPDATE paths so the next diff pass
// can compare against it. Calling code is expected to have already verified
// the hash does NOT match an existing row — skipping unchanged rows entirely
// is the whole point of the diff.
func (s *Store) UpsertCodeNodeFullWithHash(ctx context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, language string, prNumber int, returnType, params, visibility string, isAsync bool, receiverType, scope, contentHash string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language, pr_number, return_type, params, visibility, is_async, receiver_type, scope, content_hash, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, 0), $9, $10, $11, $12, $13, $14, $15, NOW())
		ON CONFLICT (repo_id, file_path, kind, name)
		DO UPDATE SET line_start = $5, line_end = $6, language = $7,
		             pr_number = COALESCE(NULLIF($8, 0), code_nodes.pr_number),
		             return_type = $9, params = $10, visibility = $11,
		             is_async = $12, receiver_type = $13, scope = $14,
		             content_hash = $15,
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

// GetBlastRadius finds all nodes transitively depending on the given file paths
// up to maxDepth hops via a recursive CTE.
func (s *Store) GetBlastRadius(ctx context.Context, repoID int64, filePaths []string, maxDepth int) ([]CodeNode, error) {
	if len(filePaths) == 0 {
		return []CodeNode{}, nil
	}
	rows, err := s.Pool.Query(ctx, `
		WITH RECURSIVE affected AS (
			SELECT id, name, file_path, kind, 0 as depth
			FROM code_nodes WHERE repo_id = $1 AND file_path = ANY($2)
			UNION
			SELECT cn.id, cn.name, cn.file_path, cn.kind, a.depth + 1
			FROM code_nodes cn
			JOIN code_edges ce ON ce.source_id = cn.id
			JOIN affected a ON ce.target_id = a.id
			WHERE a.depth < $3 AND cn.repo_id = $1
		)
		SELECT DISTINCT id, name, file_path, kind, depth FROM affected ORDER BY depth, file_path LIMIT 50
	`, repoID, filePaths, maxDepth)
	if err != nil {
		return nil, fmt.Errorf("blast radius query: %w", err)
	}
	defer rows.Close()
	return collectOrEmpty(rows, func(row pgx.CollectableRow) (CodeNode, error) {
		var n CodeNode
		err := row.Scan(&n.ID, &n.Name, &n.FilePath, &n.Kind, &n.Depth)
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
		SELECT DISTINCT p.id, p.installation_id, p.repo_id, p.content, p.supermemory_id,
		       p.created_by, COALESCE(p.source, 'manual'), p.category, p.pr_number, p.created_at, p.updated_at
		FROM patterns p
		JOIN review_comments rc ON rc.matched_pattern_id = p.id
		JOIN reviews r ON r.id = rc.review_id
		WHERE rc.file_path = $1 AND r.repo_id = $2
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

	// Recent comments on this file
	cRows, err := s.Pool.Query(ctx, `
		SELECT rc.id, rc.review_id, rc.file_path, rc.start_line, rc.end_line, rc.side,
		       rc.body, rc.severity, rc.category, rc.specialist, rc.confidence_score,
		       rc.code_snippet, rc.github_comment_id, rc.matched_pattern_id,
		       rc.matched_pattern_score, rc.enforced_rule_content, rc.is_new_finding, rc.created_at,
		       rc.state, rc.suppressed_reason
		FROM review_comments rc
		JOIN reviews r ON r.id = rc.review_id
		WHERE rc.file_path = $1 AND r.repo_id = $2
		ORDER BY rc.created_at DESC LIMIT 5
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
