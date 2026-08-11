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

// MemoryMirrorPatternIdentity is the relational identity needed to reconstruct
// a pre-mirror pattern's deterministic custom ID. Legacy rows have neither
// memory_custom_id nor memory_doc_id, so the memory package remains the single
// authority for deriving their ID from this projection.
type MemoryMirrorPatternIdentity struct {
	ID       int64
	Repo     string
	Shared   bool
	Content  string
	Source   string
	Category string
}

// MemoryMirrorLegacyOwner reports whether a legacy pattern owns the custom ID
// currently being processed. It runs while the store holds the custom-ID lock.
type MemoryMirrorLegacyOwner func(MemoryMirrorPatternIdentity) (bool, error)

// MemoryMirrorApply performs the idempotent external memory operation. For a
// pattern delete, deleteAuthorized is false while another relational pattern
// still owns the same deterministic custom ID.
type MemoryMirrorApply func(context.Context, bool) error

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
	customID := mirrorEventCustomID(event.Payload)
	if customID != "" {
		if err := lockMemoryMirrorCustomID(ctx, tx, event.InstallationID, customID); err != nil {
			return err
		}
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

// ClaimMemoryMirrorEvents leases the oldest ready event for each custom ID.
// Aggregate ordering remains as a fallback, and an unbound legacy pattern is a
// conservative installation-wide pattern barrier until the worker reconstructs
// and binds its ID. Earlier transitions therefore cannot finish after later
// transitions when multiple workers use SKIP LOCKED concurrently.
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
                  WHERE earlier.installation_id = o.installation_id
                    AND earlier.id < o.id
                    AND earlier.processed_at IS NULL
                    AND (
                      (earlier.aggregate_type = o.aggregate_type
                       AND earlier.aggregate_id = o.aggregate_id)
                      OR NULLIF(earlier.payload->>'custom_id', '') = NULLIF(o.payload->>'custom_id', '')
                      OR (earlier.aggregate_type = 'pattern'
                          AND NULLIF(earlier.payload->>'custom_id', '') IS NULL
                          AND o.aggregate_type = 'pattern')
                    )
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

func mirrorEventCustomID(payload json.RawMessage) string {
	var identity struct {
		CustomID string `json:"custom_id"`
	}
	if err := json.Unmarshal(payload, &identity); err != nil {
		return ""
	}
	return identity.CustomID
}

func lockMemoryMirrorCustomID(ctx context.Context, tx pgx.Tx, installationID int64, customID string) error {
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("memory-mirror:%d:%s", installationID, customID)); err != nil {
		return fmt.Errorf("lock memory mirror custom ID: %w", err)
	}
	return nil
}

// ProcessMemoryMirrorEvent serializes the external side effect, relational
// ownership check, and lease acknowledgement for one custom ID. Producers take
// the same transaction-scoped advisory lock before committing a new event, so
// a pattern cannot become a live owner in the check-to-delete interval.
//
// External operations are deliberately inside the transaction. If the process
// dies after the external write, the transaction rolls back and stale-lease
// replay repeats the idempotent operation. If commit succeeds, the CAS
// acknowledgement and operation are ordered together for every machine.
func (s *Store) ProcessMemoryMirrorEvent(
	ctx context.Context,
	event MemoryMirrorOutboxEvent,
	customID string,
	legacyOwner MemoryMirrorLegacyOwner,
	apply MemoryMirrorApply,
) error {
	if customID == "" {
		return fmt.Errorf("process memory mirror event %d: empty custom ID", event.ID)
	}
	if apply == nil {
		return fmt.Errorf("process memory mirror event %d: nil apply callback", event.ID)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("process memory mirror event %d: begin: %w", event.ID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockMemoryMirrorCustomID(ctx, tx, event.InstallationID, customID); err != nil {
		return fmt.Errorf("process memory mirror event %d: %w", event.ID, err)
	}
	var leased bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM memory_mirror_outbox
			WHERE id = $1 AND processed_at IS NULL AND claimed_at = $2
			FOR UPDATE
		)`, event.ID, event.ClaimedAt).Scan(&leased); err != nil {
		return fmt.Errorf("process memory mirror event %d: verify lease: %w", event.ID, err)
	}
	if !leased {
		return fmt.Errorf("process memory mirror event %d: lease lost", event.ID)
	}

	deleteAuthorized := true
	if event.AggregateType == MemoryMirrorPattern && event.Operation == MemoryMirrorDelete {
		deleteAuthorized, err = patternMirrorDeleteAuthorized(ctx, tx, event.InstallationID, customID, legacyOwner)
		if err != nil {
			return fmt.Errorf("process memory mirror event %d: check pattern owner: %w", event.ID, err)
		}
	}
	if err := apply(ctx, deleteAuthorized); err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE memory_mirror_outbox
		SET processed_at = now(), claimed_at = NULL, last_error = NULL, updated_at = now()
		WHERE id = $1 AND processed_at IS NULL AND claimed_at = $2`, event.ID, event.ClaimedAt)
	if err != nil {
		return fmt.Errorf("process memory mirror event %d: acknowledge: %w", event.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("process memory mirror event %d: lease lost", event.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("process memory mirror event %d: commit: %w", event.ID, err)
	}
	return nil
}

func patternMirrorDeleteAuthorized(
	ctx context.Context,
	tx pgx.Tx,
	installationID int64,
	customID string,
	legacyOwner MemoryMirrorLegacyOwner,
) (bool, error) {
	var explicitlyOwned bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM patterns
			WHERE installation_id = $1
			  AND COALESCE(NULLIF(memory_custom_id, ''), NULLIF(memory_doc_id, '')) = $2
		)`, installationID, customID).Scan(&explicitlyOwned); err != nil {
		return false, err
	}
	if explicitlyOwned {
		return false, nil
	}
	if legacyOwner == nil {
		return true, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT p.id, COALESCE(split_part(r.full_name, '/', 2), ''), p.repo_id IS NULL,
		       p.content, COALESCE(p.source, 'manual'), COALESCE(p.category, '')
		FROM patterns p
		LEFT JOIN repos r ON r.id = p.repo_id AND r.installation_id = p.installation_id
		WHERE p.installation_id = $1
		  AND NULLIF(p.memory_custom_id, '') IS NULL
		  AND NULLIF(p.memory_doc_id, '') IS NULL`, installationID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var identity MemoryMirrorPatternIdentity
		if err := rows.Scan(&identity.ID, &identity.Repo, &identity.Shared, &identity.Content, &identity.Source, &identity.Category); err != nil {
			return false, err
		}
		owns, err := legacyOwner(identity)
		if err != nil {
			return false, err
		}
		if owns {
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return true, nil
}

// BindMemoryMirrorEventCustomID persists a reconstructed legacy identity under
// the active lease. Once bound, a retry blocks only transitions for the same
// custom ID instead of conservatively blocking every pattern transition in the
// installation behind an unknown legacy identity.
func (s *Store) BindMemoryMirrorEventCustomID(ctx context.Context, event MemoryMirrorOutboxEvent, customID string) error {
	if customID == "" {
		return fmt.Errorf("bind memory mirror event %d custom ID: empty custom ID", event.ID)
	}
	tag, err := s.Pool.Exec(ctx, `
		UPDATE memory_mirror_outbox
		SET payload = jsonb_set(payload, '{custom_id}', to_jsonb($3::text), true),
		    updated_at = now()
		WHERE id = $1 AND processed_at IS NULL AND claimed_at = $2
		  AND NULLIF(payload->>'custom_id', '') IS NULL`, event.ID, event.ClaimedAt, customID)
	if err != nil {
		return fmt.Errorf("bind memory mirror event %d custom ID: %w", event.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("bind memory mirror event %d custom ID: lease lost or identity already bound", event.ID)
	}
	return nil
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
