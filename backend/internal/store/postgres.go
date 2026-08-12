package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvector "github.com/pgvector/pgvector-go/pgx"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// Store wraps a PostgreSQL connection pool and sqlc-generated queries.
type Store struct {
	Pool *pgxpool.Pool
	q    *db.Queries

	// beginReviewPostTx is a narrow failure-injection seam for the transaction
	// that fences the non-idempotent GitHub review mutation. Production leaves
	// it nil and uses Pool.Begin; PostgreSQL tests wrap a real transaction to
	// simulate a lost UPDATE or COMMIT response.
	beginReviewPostTx func(context.Context, *pgxpool.Conn) (pgx.Tx, error)
	// unlockReviewPostSession injects an unconfirmed session-unlock result in
	// PostgreSQL tests. Production executes pg_advisory_unlock directly.
	unlockReviewPostSession func(context.Context, *pgxpool.Conn, string) (bool, error)
}

// NewWithDB builds a Store over any sqlc-compatible pool or transaction.
// Callers that only use generated Store methods do not need a concrete pool.
func NewWithDB(dbtx db.DBTX) *Store {
	st := &Store{q: db.New(dbtx)}
	if pool, ok := dbtx.(*pgxpool.Pool); ok {
		st.Pool = pool
	}
	return st
}

func New(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.HealthCheckPeriod = 30 * time.Second
	// pgvector types (memories.embedding) — registered per-connection so pgx
	// encodes/decodes pgvector.Vector natively instead of text literals.
	// Deliberate blast radius: registration fails when the vector extension is
	// absent, which fails EVERY connection — the app won't boot against a
	// Postgres without pgvector. That's consistent by design: migration 057
	// hard-requires the extension and runs before the app (release_command),
	// and the self-host compose image ships it. A pgvector-less database is a
	// misconfigured deployment, better failed loudly at startup than degraded
	// quietly at review time.
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		return pgxvector.RegisterTypes(ctx, conn)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	st := &Store{Pool: pool, q: db.New(pool)}
	go st.keepAlive()
	return st, nil
}

// keepAlive pings the DB every 4 minutes to prevent Neon cold starts.
func (s *Store) keepAlive() {
	ticker := time.NewTicker(4 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.Pool.Ping(ctx); err != nil {
			slog.Warn("db keepalive ping failed", "error", err)
		}
		cancel()
	}
}

func (s *Store) Close() {
	s.Pool.Close()
}
