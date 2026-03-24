package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

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
