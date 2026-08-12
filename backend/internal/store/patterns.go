package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
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
	rows, err := s.q.ListPatterns(ctx, installationIDs)
	if err != nil {
		return nil, err
	}
	patterns := make([]Pattern, 0, len(rows))
	for _, row := range rows {
		pattern, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

// ListPatternsForRepo returns org-wide patterns (repo_id IS NULL) plus patterns scoped to the given repo.
func (s *Store) ListPatternsForRepo(ctx context.Context, installationIDs []int64, repoID int64) ([]Pattern, error) {
	rows, err := s.q.ListPatternsForRepo(ctx, db.ListPatternsForRepoParams{Column1: installationIDs, RepoID: &repoID})
	if err != nil {
		return nil, err
	}
	patterns := make([]Pattern, 0, len(rows))
	for _, row := range rows {
		pattern, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, pattern)
	}
	return patterns, nil
}

// CreatePattern inserts a pattern and its memory-mirror event atomically.
// Callers must supply a deterministic memoryCustomID (or a legacy memoryDocID)
// so a committed relational row always has a retryable mirror identity.
// mirrorExtra carries provenance that the patterns table does not model but a
// repaired memory document still needs (for example a shared pattern's full
// owner/repo origin).
func (s *Store) CreatePattern(ctx context.Context, installationID int64, repoID *int64, content string, memoryDocID *string, createdBy *string, source *string, category *string, prNumber *int, memoryCustomID *string, mirrorExtra map[string]string) (*Pattern, error) {
	customID := firstNonEmpty(memoryCustomID, memoryDocID)
	if customID == "" {
		return nil, fmt.Errorf("creating pattern requires a deterministic memory identity")
	}

	var pattern Pattern
	err := s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		q := db.New(tx)
		repo := ""
		if repoID != nil {
			repoRow, err := q.GetRepoScoped(ctx, db.GetRepoScopedParams{ID: *repoID, Column2: []int64{installationID}})
			if err != nil {
				return MemoryMirrorEvent{}, fmt.Errorf("resolve pattern repo: %w", err)
			}
			_, repo, _ = strings.Cut(repoRow.FullName, "/")
			if repo == "" {
				return MemoryMirrorEvent{}, fmt.Errorf("repo %d has invalid full name", *repoID)
			}
		}

		row, err := q.CreatePattern(ctx, db.CreatePatternParams{
			InstallationID: installationID, RepoID: repoID, Content: content,
			MemoryDocID: memoryDocID, CreatedBy: createdBy, Source: source,
			Category: category, PRNumber: prNumber, MemoryCustomID: memoryCustomID,
		})
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		pattern, err = patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		payload, err := newPatternOutboxPayload(pattern, customID, repo, mirrorExtra)
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		return MemoryMirrorEvent{
			InstallationID: installationID,
			AggregateType:  MemoryMirrorPattern,
			AggregateID:    pattern.ID,
			Operation:      MemoryMirrorUpsert,
			Payload:        payload,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return &pattern, nil
}

func (s *Store) DeletePattern(ctx context.Context, id int64, installationIDs []int64) error {
	return s.WithMemoryMirrorTx(ctx, func(tx pgx.Tx) (MemoryMirrorEvent, error) {
		q := db.New(tx)
		row, err := q.DeletePattern(ctx, db.DeletePatternParams{ID: id, InstallationIds: installationIDs})
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryMirrorEvent{}, fmt.Errorf("pattern not found")
		}
		if err != nil {
			return MemoryMirrorEvent{}, err
		}

		repo := ""
		if row.RepoID != nil {
			repoRow, err := q.GetRepoScoped(ctx, db.GetRepoScopedParams{ID: *row.RepoID, Column2: []int64{row.InstallationID}})
			if err != nil {
				return MemoryMirrorEvent{}, fmt.Errorf("resolve deleted pattern repo: %w", err)
			}
			_, repo, _ = strings.Cut(repoRow.FullName, "/")
			if repo == "" {
				return MemoryMirrorEvent{}, fmt.Errorf("repo %d has invalid full name", *row.RepoID)
			}
		}

		// Legacy rows can predate both identity columns. Keep the full pattern
		// projection in the tombstone so the memory worker can reconstruct the
		// deterministic ID after the relational row is gone.
		customID := firstNonEmpty(row.MemoryCustomID, row.MemoryDocID)
		pattern := Pattern{
			ID: id, InstallationID: row.InstallationID, RepoID: row.RepoID,
			Content: row.Content, Source: row.Source, Category: row.Category,
			PRNumber: row.PRNumber,
		}
		payload, err := newPatternOutboxPayload(pattern, customID, repo, nil)
		if err != nil {
			return MemoryMirrorEvent{}, err
		}
		return MemoryMirrorEvent{
			InstallationID: row.InstallationID,
			AggregateType:  MemoryMirrorPattern,
			AggregateID:    id,
			Operation:      MemoryMirrorDelete,
			Payload:        payload,
		}, nil
	})
}

func firstNonEmpty(values ...*string) string {
	for _, value := range values {
		if value != nil && *value != "" {
			return *value
		}
	}
	return ""
}

func newPatternOutboxPayload(pattern Pattern, customID, repo string, extra map[string]string) (json.RawMessage, error) {
	payload := struct {
		CustomID string `json:"custom_id"`
		Repo     string `json:"repo,omitempty"`
		Shared   bool   `json:"shared,omitempty"`
		Pattern  struct {
			Content  string
			CustomID string
			Source   string
			Category string
			PRNumber int
			Extra    map[string]string
		} `json:"pattern"`
	}{CustomID: customID, Repo: repo, Shared: pattern.RepoID == nil}
	payload.Pattern.Content = pattern.Content
	payload.Pattern.CustomID = customID
	payload.Pattern.Source = pattern.Source
	payload.Pattern.Extra = extra
	if pattern.Category != nil {
		payload.Pattern.Category = *pattern.Category
	}
	if pattern.PRNumber != nil {
		payload.Pattern.PRNumber = *pattern.PRNumber
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal pattern mirror payload: %w", err)
	}
	return raw, nil
}

func (s *Store) GetPattern(ctx context.Context, id int64) (*Pattern, error) {
	row, err := s.q.GetPattern(ctx, id)
	if err != nil {
		return nil, err
	}
	pattern, err := patternFromSQLC(row.ID, row.InstallationID, row.RepoID, row.Content, row.MemoryDocID, row.CreatedBy, row.Source, row.Category, row.PRNumber, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &pattern, nil
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
	rows, err := s.q.GetPatternStats(ctx, installationIDs)
	if err != nil {
		return nil, err
	}
	stats := make([]PatternStat, 0, len(rows))
	for _, row := range rows {
		stats = append(stats, PatternStat{Week: row.Week, Source: row.Source, Count: row.Count})
	}
	return stats, nil
}
