// Migrate applies pending DB migrations and exits. Wired into Fly's release_command so
// migrations run before new machines receive traffic. Reads DATABASE_URL; fails fast on
// error; re-runs are no-ops.
package main

import (
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store/migrations"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// logVersion reads the current migration version and logs any read error. Used both before
// and after Up() — a silent discard would hide Neon idle-kill / transient connectivity blips
// and could mislead operators into thinking migrations ran from the wrong base version.
func logVersion(m *migrate.Migrate, logger *slog.Logger) (version uint, dirty bool) {
	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		logger.Warn("read migration version", "error", err)
	}
	return version, dirty
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
	started := time.Now()
	logger.Info("migration command started")

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	driverStarted := time.Now()
	logger.Info("embedded migration source initialization started")
	driver, err := iofs.New(migrations.FS, ".")
	if err != nil {
		logger.Error("embedded migration source initialization failed",
			"duration_ms", time.Since(driverStarted).Milliseconds(), "error", err)
		os.Exit(1)
	}
	logger.Info("embedded migration source initialization completed",
		"duration_ms", time.Since(driverStarted).Milliseconds())

	migratorStarted := time.Now()
	logger.Info("database migrator initialization started")
	m, err := migrate.NewWithSourceInstance("iofs", driver, dbURL)
	if err != nil {
		logger.Error("database migrator initialization failed",
			"duration_ms", time.Since(migratorStarted).Milliseconds(), "error", err)
		os.Exit(1)
	}
	logger.Info("database migrator initialization completed",
		"duration_ms", time.Since(migratorStarted).Milliseconds())
	defer func() {
		closeStarted := time.Now()
		logger.Info("database migrator close started")
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			logger.Warn("database migrator close failed", "duration_ms", time.Since(closeStarted).Milliseconds(),
				"source_error", srcErr, "database_error", dbErr)
		} else {
			logger.Info("database migrator close completed", "duration_ms", time.Since(closeStarted).Milliseconds())
		}
	}()

	logger.Info("migration version read started", "phase", "before")
	before, dirtyBefore := logVersion(m, logger)
	logger.Info("migration version read completed", "phase", "before", "version", before, "dirty", dirtyBefore)

	upStarted := time.Now()
	logger.Info("migration apply started", "from_version", before, "dirty", dirtyBefore)
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("migration apply completed", "outcome", "no_change", "at_version", before,
				"duration_ms", time.Since(upStarted).Milliseconds())
			logger.Info("migration command completed", "duration_ms", time.Since(started).Milliseconds())
			return
		}
		// ErrLocked (concurrent migrator) and ErrDirty (previous run partially failed) fall
		// through to this branch deliberately. Both require operator attention — auto-recovery
		// from a dirty migration is unsafe, and a stuck advisory lock likely means another
		// deploy is in flight. Aborting the deploy is the correct response.
		logger.Error("migration apply failed", "from_version", before,
			"duration_ms", time.Since(upStarted).Milliseconds(), "error", err)
		os.Exit(1)
	}

	logger.Info("migration version read started", "phase", "after")
	after, dirty := logVersion(m, logger)
	logger.Info("migration version read completed", "phase", "after", "version", after, "dirty", dirty)
	logger.Info("migration apply completed", "outcome", "applied", "from_version", before,
		"to_version", after, "dirty", dirty, "duration_ms", time.Since(upStarted).Milliseconds())
	logger.Info("migration command completed", "duration_ms", time.Since(started).Milliseconds())
}
