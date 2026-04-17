// Migrate runs all pending DB migrations and exits. Wired into Fly's release_command so
// migrations land automatically on every deploy — fixes the class of bug where we shipped
// migration 036 but the column didn't exist on prod because nobody remembered to run it.
//
// Reads DATABASE_URL from env, fails fast on any migration error. Safe to re-run; `migrate up`
// is a no-op when there are no pending files.
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

	before, _, _ := m.Version()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("no pending migrations", "at_version", before)
			return
		}
		logger.Error("migrate up failed", "error", err)
		os.Exit(1)
	}

	after, dirty, _ := m.Version()
	logger.Info("migrations applied", "from", before, "to", after, "dirty", dirty)
}
