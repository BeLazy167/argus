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
func (s *Store) UpsertCodeNode(ctx context.Context, repoID int64, kind, name, filePath string, lineStart, lineEnd int, language string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (repo_id, file_path, kind, name)
		DO UPDATE SET line_start = $5, line_end = $6, language = $7, updated_at = NOW()
		RETURNING id
	`, repoID, kind, name, filePath, lineStart, lineEnd, language).Scan(&id)
	return id, err
}

// UpsertCodeEdge inserts a code edge, ignoring duplicates.
func (s *Store) UpsertCodeEdge(ctx context.Context, repoID, sourceID, targetID int64, kind string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO code_edges (repo_id, source_id, target_id, kind, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (repo_id, source_id, target_id, kind) DO NOTHING
	`, repoID, sourceID, targetID, kind)
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
		       rc.matched_pattern_score, rc.enforced_rule_content, rc.is_new_finding, rc.created_at
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
