package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	MemoryMirrorPattern = "pattern"
	MemoryMirrorRule    = "rule"
	MemoryMirrorUpsert  = "upsert"
	MemoryMirrorDelete  = "delete"
)

type MemoryMirrorEvent struct {
	InstallationID int64
	AggregateType  string
	AggregateID    int64
	Operation      string
	Payload        json.RawMessage
}

type MemoryMirrorOutboxEvent struct {
	ID             int64           `db:"id"`
	InstallationID int64           `db:"installation_id"`
	AggregateType  string          `db:"aggregate_type"`
	AggregateID    int64           `db:"aggregate_id"`
	Operation      string          `db:"operation"`
	Payload        json.RawMessage `db:"payload"`
	AttemptCount   int             `db:"attempt_count"`
	AvailableAt    time.Time       `db:"available_at"`
	ClaimedAt      time.Time       `db:"claimed_at"`
	CreatedAt      time.Time       `db:"created_at"`
}

func (e MemoryMirrorEvent) validate() error {
	if e.InstallationID <= 0 || e.AggregateID <= 0 {
		return fmt.Errorf("mirror event requires installation and aggregate ids")
	}
	if e.AggregateType != MemoryMirrorPattern && e.AggregateType != MemoryMirrorRule {
		return fmt.Errorf("invalid mirror aggregate type %q", e.AggregateType)
	}
	if e.Operation != MemoryMirrorUpsert && e.Operation != MemoryMirrorDelete {
		return fmt.Errorf("invalid mirror operation %q", e.Operation)
	}
	if !json.Valid(e.Payload) {
		return fmt.Errorf("mirror payload must be valid JSON")
	}
	return nil
}

// WithMemoryMirrorTx commits a relational mutation and its mirror event as one
// transaction. The callback performs the source-row mutation and returns the
// event describing the resulting desired memory state.
func (s *Store) WithMemoryMirrorTx(ctx context.Context, fn func(pgx.Tx) (MemoryMirrorEvent, error)) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin memory mirror transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	event, err := fn(tx)
	if err != nil {
		return err
	}
	if err := enqueueMemoryMirrorEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit memory mirror transaction: %w", err)
	}
	return nil
}

func enqueueMemoryMirrorEvent(ctx context.Context, tx pgx.Tx, event MemoryMirrorEvent) error {
	if err := event.validate(); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
        INSERT INTO memory_mirror_outbox
            (installation_id, aggregate_type, aggregate_id, operation, payload)
        VALUES ($1, $2, $3, $4, $5)`,
		event.InstallationID, event.AggregateType, event.AggregateID, event.Operation, event.Payload)
	if err != nil {
		return fmt.Errorf("enqueue memory mirror event: %w", err)
	}
	return nil
}

// ClaimMemoryMirrorEvents leases the oldest ready event for each aggregate.
// Earlier unprocessed transitions block later ones, preserving source order
// even when multiple workers use SKIP LOCKED concurrently.
func (s *Store) ClaimMemoryMirrorEvents(ctx context.Context, limit int, staleAfter time.Duration) ([]MemoryMirrorOutboxEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if staleAfter <= 0 {
		staleAfter = 5 * time.Minute
	}
	rows, err := s.Pool.Query(ctx, `
        WITH candidates AS (
            SELECT o.id
            FROM memory_mirror_outbox o
            WHERE o.processed_at IS NULL
              AND o.available_at <= now()
              AND (o.claimed_at IS NULL OR o.claimed_at <= now() - ($2 * interval '1 second'))
              AND NOT EXISTS (
                  SELECT 1 FROM memory_mirror_outbox earlier
                  WHERE earlier.aggregate_type = o.aggregate_type
                    AND earlier.aggregate_id = o.aggregate_id
                    AND earlier.id < o.id
                    AND earlier.processed_at IS NULL
              )
            ORDER BY o.id
            FOR UPDATE SKIP LOCKED
            LIMIT $1
        )
        UPDATE memory_mirror_outbox o
        SET claimed_at = now(), attempt_count = o.attempt_count + 1, updated_at = now()
        FROM candidates c
        WHERE o.id = c.id
        RETURNING o.id, o.installation_id, o.aggregate_type, o.aggregate_id,
                  o.operation, o.payload, o.attempt_count, o.available_at,
                  o.claimed_at, o.created_at`, limit, staleAfter.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim memory mirror events: %w", err)
	}
	defer rows.Close()
	events, err := collectOrEmpty(rows, pgx.RowToStructByName[MemoryMirrorOutboxEvent])
	if err != nil {
		return nil, fmt.Errorf("read claimed memory mirror events: %w", err)
	}
	return events, nil
}

func (s *Store) MarkMemoryMirrorEventProcessed(ctx context.Context, event MemoryMirrorOutboxEvent) error {
	tag, err := s.Pool.Exec(ctx, `
        UPDATE memory_mirror_outbox
        SET processed_at = now(), claimed_at = NULL, last_error = NULL, updated_at = now()
        WHERE id = $1 AND processed_at IS NULL AND claimed_at = $2`, event.ID, event.ClaimedAt)
	if err != nil {
		return fmt.Errorf("mark memory mirror event %d processed: %w", event.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("mark memory mirror event %d processed: lease lost", event.ID)
	}
	return nil
}

func (s *Store) MarkMemoryMirrorEventFailed(ctx context.Context, event MemoryMirrorOutboxEvent, cause error) error {
	retrySeconds := memoryMirrorRetryBackoff(event.AttemptCount).Seconds()
	tag, err := s.Pool.Exec(ctx, `
		UPDATE memory_mirror_outbox
		SET claimed_at = NULL,
		    available_at = now() + ($3 * interval '1 second'),
		    last_error = left($4, 4000), updated_at = now()
		WHERE id = $1 AND processed_at IS NULL AND claimed_at = $2`,
		event.ID, event.ClaimedAt, retrySeconds, cause.Error())
	if err != nil {
		return fmt.Errorf("mark memory mirror event %d failed: %w", event.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("mark memory mirror event %d failed: lease lost", event.ID)
	}
	return nil
}

func memoryMirrorRetryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	for i := 1; i < attempt && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}
