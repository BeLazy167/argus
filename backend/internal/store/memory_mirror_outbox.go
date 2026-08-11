package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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

// MemoryMirrorApply performs an idempotent external memory operation. For a
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
	isRuleTombstone := event.AggregateType == MemoryMirrorRule && event.Operation == MemoryMirrorDelete
	if isRuleTombstone {
		// The relational disable/delete is a synchronous visibility boundary.
		// Take the worker's execution key after the general producer key so an
		// older in-flight upsert finishes before this transaction tombstones it.
		// Rule workers never take the general producer key; ordinary pattern
		// producers never take this execution key and remain nonblocking.
		if customID == "" {
			return fmt.Errorf("enqueue rule delete mirror event: empty custom ID")
		}
		if err := lockMemoryMirrorExecution(ctx, tx, event.InstallationID, customID); err != nil {
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

	// A rule owns its custom ID uniquely, so disabling or deleting it can make
	// the Postgres read model unsearchable in the producer transaction. Pattern
	// custom IDs may have duplicate relational owners and must remain on the
	// outbox worker's ownership-aware delete path instead.
	if isRuleTombstone {
		if _, err := tx.Exec(ctx, `
			UPDATE memories
			SET deleted_at = now(), updated_at = now()
			WHERE installation_id = $1 AND custom_id = $2 AND deleted_at IS NULL`,
			event.InstallationID, customID); err != nil {
			return fmt.Errorf("tombstone rule memory %s: %w", customID, err)
		}
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
                          AND o.aggregate_type = 'pattern'
                          AND (NULLIF(earlier.payload->>'custom_id', '') IS NULL
                               OR NULLIF(o.payload->>'custom_id', '') IS NULL))
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

func memoryMirrorExecutionTenant(installationID int64) string {
	return fmt.Sprintf("memory-mirror-execution:%d", installationID)
}

func lockMemoryMirrorExecution(ctx context.Context, tx pgx.Tx, installationID int64, customID string) error {
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		memoryMirrorExecutionTenant(installationID), customID); err != nil {
		return fmt.Errorf("lock memory mirror execution: %w", err)
	}
	return nil
}

// ProcessMemoryMirrorEvent serializes all external side effects and their lease
// acknowledgements for one tenant/custom ID. The session execution lock uses
// PostgreSQL's two-int advisory-lock key space, which does not overlap the
// general producer transaction lock's bigint key space. Rule tombstone
// producers additionally take this execution key as a transaction lock.
//
// Slow upserts hold one bounded worker pool connection but no database
// transaction or general producer lock. Pattern deletes acquire locks in one
// order: execution lock, then producer transaction lock. This keeps the
// ownership check, quick tombstone, and acknowledgement atomic without letting
// embedding or network latency block ordinary relational producers.
func (s *Store) ProcessMemoryMirrorEvent(
	ctx context.Context,
	event MemoryMirrorOutboxEvent,
	customID string,
	legacyOwner MemoryMirrorLegacyOwner,
	apply MemoryMirrorApply,
) (returnErr error) {
	if customID == "" {
		return fmt.Errorf("process memory mirror event %d: empty custom ID", event.ID)
	}
	if apply == nil {
		return fmt.Errorf("process memory mirror event %d: nil apply callback", event.ID)
	}
	if s.Pool == nil {
		return fmt.Errorf("process memory mirror event %d: postgres pool unavailable", event.ID)
	}

	lockTenant := memoryMirrorExecutionTenant(event.InstallationID)
	conn, err := acquireMirrorExecutionConn(ctx, s.Pool, lockTenant, customID)
	if err != nil {
		return fmt.Errorf("process memory mirror event %d: acquire execution lock: %w", event.ID, err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), memoryMirrorUnlockTimeout)
		defer cancel()
		var unlocked bool
		unlockErr := conn.QueryRow(unlockCtx,
			`SELECT pg_advisory_unlock(hashtext($1), hashtext($2))`, lockTenant, customID).Scan(&unlocked)
		if unlockErr == nil && unlocked {
			conn.Release()
			return
		}
		// Closing a suspect session releases all of its advisory locks and keeps
		// it out of the pool even when cancellation or a network fault obscures
		// the unlock result.
		closeMirrorExecutionConn(conn)
		if unlockErr == nil {
			unlockErr = errors.New("execution lock was not held by its session")
		}
		returnErr = errors.Join(returnErr,
			fmt.Errorf("process memory mirror event %d: release execution lock: %w", event.ID, unlockErr))
	}()

	if event.AggregateType == MemoryMirrorPattern && event.Operation == MemoryMirrorDelete {
		return s.processMemoryMirrorPatternDelete(ctx, conn, event, customID, legacyOwner, apply)
	}

	var leased bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM memory_mirror_outbox
			WHERE id = $1 AND processed_at IS NULL AND claimed_at = $2
		)`, event.ID, event.ClaimedAt).Scan(&leased); err != nil {
		return fmt.Errorf("process memory mirror event %d: verify lease: %w", event.ID, err)
	}
	if !leased {
		return fmt.Errorf("process memory mirror event %d: lease lost", event.ID)
	}
	if err := apply(ctx, true); err != nil {
		return err
	}
	return acknowledgeMemoryMirrorEvent(ctx, conn, event)
}

const (
	memoryMirrorLockRetryDelay = 10 * time.Millisecond
	memoryMirrorUnlockTimeout  = 5 * time.Second
)

func acquireMirrorExecutionConn(ctx context.Context, pool *pgxpool.Pool, lockTenant, customID string) (*pgxpool.Conn, error) {
	for {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return nil, err
		}
		var locked bool
		if err := conn.QueryRow(ctx,
			`SELECT pg_try_advisory_lock(hashtext($1), hashtext($2))`, lockTenant, customID).Scan(&locked); err != nil {
			// A query error can hide whether the server acquired the session lock.
			// Discarding the connection makes either outcome safe.
			closeMirrorExecutionConn(conn)
			return nil, err
		}
		if locked {
			return conn, nil
		}
		// A blocking advisory-lock call would pin one pool connection per stale
		// worker. Polling without a checked-out connection leaves capacity for
		// the lock holder's memory write and for relational producers.
		conn.Release()
		timer := time.NewTimer(memoryMirrorLockRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func closeMirrorExecutionConn(conn *pgxpool.Conn) {
	closeCtx, cancel := context.WithTimeout(context.Background(), memoryMirrorUnlockTimeout)
	defer cancel()
	_ = conn.Hijack().Close(closeCtx)
}

func (s *Store) processMemoryMirrorPatternDelete(
	ctx context.Context,
	conn *pgxpool.Conn,
	event MemoryMirrorOutboxEvent,
	customID string,
	legacyOwner MemoryMirrorLegacyOwner,
	apply MemoryMirrorApply,
) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("process memory mirror event %d: begin delete: %w", event.ID, err)
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
	deleteAuthorized, err := patternMirrorDeleteAuthorized(ctx, tx, event.InstallationID, customID, legacyOwner)
	if err != nil {
		return fmt.Errorf("process memory mirror event %d: check pattern owner: %w", event.ID, err)
	}
	if err := apply(ctx, deleteAuthorized); err != nil {
		return err
	}
	if err := acknowledgeMemoryMirrorEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("process memory mirror event %d: commit delete: %w", event.ID, err)
	}
	return nil
}

type memoryMirrorAcknowledgementDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func acknowledgeMemoryMirrorEvent(ctx context.Context, db memoryMirrorAcknowledgementDB, event MemoryMirrorOutboxEvent) error {
	tag, err := db.Exec(ctx, `
		UPDATE memory_mirror_outbox
		SET processed_at = now(), claimed_at = NULL, last_error = NULL, updated_at = now()
		WHERE id = $1 AND processed_at IS NULL AND claimed_at = $2`, event.ID, event.ClaimedAt)
	if err != nil {
		return fmt.Errorf("process memory mirror event %d: acknowledge: %w", event.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("process memory mirror event %d: lease lost", event.ID)
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
