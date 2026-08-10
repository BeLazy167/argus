package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Pattern struct {
	ID             int64     `json:"id"`
	InstallationID int64     `json:"installation_id"`
	RepoID         *int64    `json:"repo_id,omitempty"`
	Content        string    `json:"content"`
	MemoryDocID    *string   `json:"memory_doc_id,omitempty"`
	CreatedBy      *string   `json:"created_by,omitempty"`
	Source         string    `json:"source"`
	Category       *string   `json:"category,omitempty"`
	PRNumber       *int      `json:"pr_number,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type PatternStat struct {
	Week   time.Time `json:"week"`
	Source string    `json:"source"`
	Count  int       `json:"count"`
}

func (s *Store) ListPatterns(ctx context.Context, installationIDs []int64) ([]Pattern, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, installation_id, repo_id, content, memory_doc_id, created_by, COALESCE(source, 'manual'), category, pr_number, created_at, updated_at
		 FROM patterns WHERE installation_id = ANY($1) ORDER BY created_at DESC`, installationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Pattern])
}

// ListPatternsForRepo returns org-wide patterns (repo_id IS NULL) plus patterns scoped to the given repo.
func (s *Store) ListPatternsForRepo(ctx context.Context, installationIDs []int64, repoID int64) ([]Pattern, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, installation_id, repo_id, content, memory_doc_id, created_by, COALESCE(source, 'manual'), category, pr_number, created_at, updated_at
		 FROM patterns WHERE installation_id = ANY($1) AND (repo_id IS NULL OR repo_id = $2) ORDER BY created_at DESC`, installationIDs, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[Pattern])
}

// CreatePattern inserts a pattern row. memoryCustomID is the deterministic
// customId mirrored from the memory write (nil when unknown); it durably
// keys the row so the per-finding enrich read can resolve a search hit back to
// this pattern by customId even when the hit's own id is a chunk id.
func (s *Store) CreatePattern(ctx context.Context, installationID int64, repoID *int64, content string, memoryDocID *string, createdBy *string, source *string, category *string, prNumber *int, memoryCustomID *string) (*Pattern, error) {
	var p Pattern
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO patterns (installation_id, repo_id, content, memory_doc_id, created_by, source, category, pr_number, memory_custom_id)
		 VALUES ($1, $2, $3, $4, $5, COALESCE($6, 'manual'), $7, $8, $9)
		 RETURNING id, installation_id, repo_id, content, memory_doc_id, created_by, COALESCE(source, 'manual'), category, pr_number, created_at, updated_at`,
		installationID, repoID, content, memoryDocID, createdBy, source, category, prNumber, memoryCustomID).
		Scan(&p.ID, &p.InstallationID, &p.RepoID, &p.Content, &p.MemoryDocID, &p.CreatedBy, &p.Source, &p.Category, &p.PRNumber, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) DeletePattern(ctx context.Context, id int64, installationIDs []int64) error {
	tag, err := s.Pool.Exec(ctx,
		`DELETE FROM patterns WHERE id = $1 AND installation_id = ANY($2)`, id, installationIDs)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("pattern not found")
	}
	return nil
}

func (s *Store) GetPattern(ctx context.Context, id int64) (*Pattern, error) {
	var p Pattern
	err := s.Pool.QueryRow(ctx,
		`SELECT id, installation_id, repo_id, content, memory_doc_id, created_by, COALESCE(source, 'manual'), category, pr_number, created_at, updated_at
		 FROM patterns WHERE id = $1`, id).
		Scan(&p.ID, &p.InstallationID, &p.RepoID, &p.Content, &p.MemoryDocID, &p.CreatedBy, &p.Source, &p.Category, &p.PRNumber, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPatternIDByMemoryDocID maps a memory pattern doc id back to its
// patterns-table row id. SearchPatternMatch returns memory docs; callers
// use this to persist review_comments.matched_pattern_id and to bump
// pattern_stats. Returns (0, pgx.ErrNoRows) when no patterns row carries that
// memory_doc_id (e.g. a synthesis/convention doc that was never mirrored to
// the patterns table) — a miss, not a failure.
// Scoped by installation: the id is a deterministic customId, not a globally
// unique server id, so two installations that learned the same pattern in
// same-named repos hold
// the SAME string here, and an unscoped LIMIT 1 with no ORDER BY can resolve
// one tenant's hit to another tenant's row — persisting a foreign
// matched_pattern_id and bumping its stats.
func (s *Store) GetPatternIDByMemoryDocID(ctx context.Context, installationID int64, memoryDocID string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		`SELECT id FROM patterns WHERE installation_id = $1 AND memory_doc_id = $2 LIMIT 1`,
		installationID, memoryDocID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetPatternIDByCustomID maps a pattern doc's deterministic customId
// back to its patterns-table row id. The per-finding enrich read prefers this
// over GetPatternIDByMemoryDocID because a hybrid-search hit's own ID may be a
// chunk id that never matches the stored memory_doc_id, whereas the customId is
// mirrored into result metadata at write time. Returns (0, pgx.ErrNoRows) when
// no row carries that customId (legacy rows written before the mirror column, or
// docs never mirrored to the patterns table) — a miss, not a failure.
// Scoped by installation for the same reason as the sibling above: customIds
// are deterministic per repo+content, so they were never globally unique.
func (s *Store) GetPatternIDByCustomID(ctx context.Context, installationID int64, customID string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		`SELECT id FROM patterns WHERE installation_id = $1 AND memory_custom_id = $2 LIMIT 1`,
		installationID, customID).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) GetPatternStats(ctx context.Context, installationIDs []int64) ([]PatternStat, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT DATE_TRUNC('week', created_at) as week, COALESCE(source, 'manual') as source, COUNT(*)::int as count
		 FROM patterns WHERE installation_id = ANY($1)
		 GROUP BY week, source ORDER BY week`, installationIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrEmpty(rows, pgx.RowToStructByPos[PatternStat])
}
