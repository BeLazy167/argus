package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ArchitectureAnnotationNode is an LLM-proposed component. It is deliberately
// not a code node: parser-owned source facts must never be mutated by an LLM.
type ArchitectureAnnotationNode struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	FilePath string `json:"file_path"`
	Language string `json:"language"`
}

// ArchitectureAnnotationEdge links two nodes inside one annotation snapshot.
type ArchitectureAnnotationEdge struct {
	ID       int64  `json:"id"`
	SourceID int64  `json:"source_id"`
	TargetID int64  `json:"target_id"`
	Kind     string `json:"kind"`
}

// ArchitectureAnnotationEdgeInput names annotation endpoints as emitted by the LLM.
type ArchitectureAnnotationEdgeInput struct {
	Source string
	Target string
	Kind   string
}

// ReplaceArchitectureAnnotations atomically replaces one PR's LLM annotation
// snapshot. Empty nodes or edges are authoritative empty snapshots.
func (s *Store) ReplaceArchitectureAnnotations(ctx context.Context, repoID int64, prNumber int, nodes []ArchitectureAnnotationNode, edges []ArchitectureAnnotationEdgeInput) (int, int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("replace architecture annotations: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM architecture_annotation_nodes WHERE repo_id = $1 AND pr_number = $2`, repoID, prNumber); err != nil {
		return 0, 0, fmt.Errorf("replace architecture annotations: delete: %w", err)
	}

	idsByName := make(map[string][]int64, len(nodes))
	seenNodeIDs := make(map[int64]struct{}, len(nodes))
	writtenNodes := 0
	for _, n := range nodes {
		if n.Name == "" || n.Kind == "" || n.FilePath == "" {
			continue
		}
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO architecture_annotation_nodes (repo_id, pr_number, kind, name, file_path, language)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (repo_id, pr_number, file_path, kind, name)
			DO UPDATE SET language = EXCLUDED.language
			RETURNING id`, repoID, prNumber, n.Kind, n.Name, n.FilePath, n.Language).Scan(&id); err != nil {
			return 0, 0, fmt.Errorf("replace architecture annotations: insert node: %w", err)
		}
		if _, duplicate := seenNodeIDs[id]; !duplicate {
			seenNodeIDs[id] = struct{}{}
			idsByName[n.Name] = append(idsByName[n.Name], id)
			writtenNodes++
		}
	}

	writtenEdges := 0
	for _, e := range edges {
		sources, targets := idsByName[e.Source], idsByName[e.Target]
		// A name that maps to more than one component is ambiguous. Dropping the
		// hypothesis is safer than inventing a connection to the first row.
		if len(sources) != 1 || len(targets) != 1 || e.Kind == "" {
			continue
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO architecture_annotation_edges (repo_id, pr_number, source_id, target_id, kind)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT DO NOTHING`, repoID, prNumber, sources[0], targets[0], e.Kind)
		if err != nil {
			return 0, 0, fmt.Errorf("replace architecture annotations: insert edge: %w", err)
		}
		writtenEdges += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, fmt.Errorf("replace architecture annotations: commit: %w", err)
	}
	return writtenNodes, writtenEdges, nil
}

// ListArchitectureAnnotations returns all LLM annotation snapshots for a repo.
func (s *Store) ListArchitectureAnnotations(ctx context.Context, repoID int64) ([]ArchitectureAnnotationNode, []ArchitectureAnnotationEdge, error) {
	nodeRows, err := s.Pool.Query(ctx, `
		SELECT id, name, kind, file_path, language
		FROM architecture_annotation_nodes WHERE repo_id = $1 ORDER BY id`, repoID)
	if err != nil {
		return nil, nil, fmt.Errorf("list architecture annotation nodes: %w", err)
	}
	nodes, err := collectOrEmpty(nodeRows, func(row pgx.CollectableRow) (ArchitectureAnnotationNode, error) {
		var n ArchitectureAnnotationNode
		err := row.Scan(&n.ID, &n.Name, &n.Kind, &n.FilePath, &n.Language)
		return n, err
	})
	nodeRows.Close()
	if err != nil {
		return nil, nil, err
	}
	edgeRows, err := s.Pool.Query(ctx, `
		SELECT id, source_id, target_id, kind
		FROM architecture_annotation_edges WHERE repo_id = $1 ORDER BY id`, repoID)
	if err != nil {
		return nil, nil, fmt.Errorf("list architecture annotation edges: %w", err)
	}
	edges, err := collectOrEmpty(edgeRows, func(row pgx.CollectableRow) (ArchitectureAnnotationEdge, error) {
		var e ArchitectureAnnotationEdge
		err := row.Scan(&e.ID, &e.SourceID, &e.TargetID, &e.Kind)
		return e, err
	})
	edgeRows.Close()
	return nodes, edges, err
}
