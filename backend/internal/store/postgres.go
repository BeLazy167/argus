package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps a PostgreSQL connection pool and sqlc-generated queries.
type Store struct {
	Pool *pgxpool.Pool
	Q    *db.Queries
}

func New(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}
	config.MaxConns = 20
	config.MinConns = 2
	config.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}
	st := &Store{Pool: pool, Q: db.New(pool)}
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
