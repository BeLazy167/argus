package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MemoryAPI is the safe public representation of a live memory. Vector data
// and lifecycle tombstones are intentionally absent from this type.
type MemoryAPI struct {
	ID             int64           `json:"id"`
	InstallationID int64           `json:"installation_id"`
	ContainerTag   string          `json:"container_tag"`
	CustomID       string          `json:"custom_id"`
	Type           string          `json:"type"`
	Content        string          `json:"content"`
	Metadata       json.RawMessage `json:"metadata"`
	ReviewID       *uuid.UUID      `json:"review_id"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// MemoryListFilter is a fully authorized memory-list request. ContainerTags
// nil means every container in the installation; a non-empty slice narrows it.
type MemoryListFilter struct {
	InstallationID int64
	ContainerTags  []string
	Type           string
	Query          string
	Limit          int
	Offset         int
}

type MemoryListResult struct {
	Memories []MemoryAPI
	Total    int
}

// ListMemories lists only live rows for one installation in deterministic
// updated_at,id order and reports the matching total before pagination.
func (s *Store) ListMemories(ctx context.Context, filter MemoryListFilter) (MemoryListResult, error) {
	where := []string{
		"installation_id = $1",
		"deleted_at IS NULL",
		"invalidated_at IS NULL",
	}
	args := []any{filter.InstallationID}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if len(filter.ContainerTags) > 0 {
		add("container_tag = ANY($%d)", filter.ContainerTags)
	}
	if filter.Type != "" {
		add("type = $%d", filter.Type)
	}
	if filter.Query != "" {
		add("content_tsv @@ websearch_to_tsquery('english', $%d)", filter.Query)
	}
	predicate := strings.Join(where, " AND ")

	var total int
	if err := s.Pool.QueryRow(ctx, "SELECT COUNT(*)::int FROM memories WHERE "+predicate, args...).Scan(&total); err != nil {
		return MemoryListResult{}, fmt.Errorf("counting memories: %w", err)
	}

	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.Pool.Query(ctx, fmt.Sprintf(`
        SELECT id, installation_id, container_tag, custom_id, type, content,
               metadata, review_id, created_at, updated_at
        FROM memories
        WHERE %s
        ORDER BY updated_at DESC, id DESC
        LIMIT $%d OFFSET $%d`, predicate, len(args)-1, len(args)), args...)
	if err != nil {
		return MemoryListResult{}, fmt.Errorf("listing memories: %w", err)
	}
	defer rows.Close()
	memories, err := collectOrEmpty(rows, pgx.RowToStructByPos[MemoryAPI])
	if err != nil {
		return MemoryListResult{}, fmt.Errorf("reading memories: %w", err)
	}
	return MemoryListResult{Memories: memories, Total: total}, nil
}
