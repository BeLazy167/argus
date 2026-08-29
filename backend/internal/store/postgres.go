package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
	pgxvector "github.com/pgvector/pgvector-go/pgx"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// Store wraps a PostgreSQL connection pool and sqlc-generated queries.
type Store struct {
	Pool *pgxpool.Pool
	q    *db.Queries

	// beginReviewPostClaimTx and beginReviewPostTx are narrow failure-injection
	// seams for the two posting transactions. Production leaves them nil and
	// uses Conn.Begin; PostgreSQL tests simulate lost claim/persistence commits.
	beginReviewPostClaimTx func(context.Context, *pgxpool.Conn) (pgx.Tx, error)
	beginReviewPostTx      func(context.Context, *pgxpool.Conn) (pgx.Tx, error)
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
	config.ConnConfig.Tracer = &tracelog.TraceLog{
		LogLevel: tracelog.LogLevelTrace,
		Logger: tracelog.LoggerFunc(func(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
			slogLevel := slog.LevelDebug
			switch level {
			case tracelog.LogLevelError:
				slogLevel = slog.LevelError
			case tracelog.LogLevelWarn:
				slogLevel = slog.LevelWarn
			case tracelog.LogLevelInfo:
				slogLevel = slog.LevelInfo
			}
			slog.Log(ctx, slogLevel, "postgres operation", "pgx_level", level.String(),
				"operation", msg, "details", sanitizedPGXDetails(data))
		}),
	}
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

// sanitizedPGXDetails preserves SQL, arguments, row counts, durations, and
// errors for query-level debugging. Arguments are withheld only for statements
// that touch credential-bearing columns/tables.
func sanitizedPGXDetails(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
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
