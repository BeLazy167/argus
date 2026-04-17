// Migrate applies pending DB migrations and exits. Wired into Fly's release_command so
// migrations run before new machines receive traffic. Reads DATABASE_URL; fails fast on
// error; re-runs are no-ops.
package main

import (
	"errors"
	"log/slog"
	"os"

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
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	driver, err := iofs.New(migrations.FS, ".")
	if err != nil {
		logger.Error("open embedded migrations", "error", err)
		os.Exit(1)
	}

	m, err := migrate.NewWithSourceInstance("iofs", driver, dbURL)
	if err != nil {
		logger.Error("init migrator", "error", err)
		os.Exit(1)
	}
	defer func() {
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			logger.Warn("migrator close", "src_err", srcErr, "db_err", dbErr)
		}
	}()

	before, _ := logVersion(m, logger)

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("no pending migrations", "at_version", before)
			return
		}
		// ErrLocked (concurrent migrator) and ErrDirty (previous run partially failed) fall
		// through to this branch deliberately. Both require operator attention — auto-recovery
		// from a dirty migration is unsafe, and a stuck advisory lock likely means another
		// deploy is in flight. Aborting the deploy is the correct response.
		logger.Error("migrate up failed", "error", err)
		os.Exit(1)
	}

	after, dirty := logVersion(m, logger)
	logger.Info("migrations applied", "from", before, "to", after, "dirty", dirty)
}
