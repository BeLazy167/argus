package store

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	storemigrations "github.com/BeLazy167/argus/backend/internal/store/migrations"
)

func TestReviewSignalMigrationPreservesOldBinaryDedup(t *testing.T) {
	baseDSN := os.Getenv("TEST_DATABASE_URL")
	if baseDSN == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatalf("connect to test Postgres: %v", err)
	}
	t.Cleanup(admin.Close)

	databaseName := "argus_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedDatabaseName := pgx.Identifier{databaseName}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quotedDatabaseName); err != nil {
		t.Fatalf("create isolated migration database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quotedDatabaseName+" WITH (FORCE)")
	})

	migrationDSN := migrationTestDatabaseURL(t, baseDSN, databaseName)
	source, err := iofs.New(storemigrations.FS, ".")
	if err != nil {
		t.Fatalf("open embedded migrations: %v", err)
	}
	migrator, err := migrate.NewWithSourceInstance("iofs", source, migrationDSN)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	defer func() {
		_, _ = migrator.Close()
	}()

	// Exercise the production rollback path from the current chain head through
	// 081, rather than applying migration 076 in isolation against a guessed schema.
	if err := migrator.Up(); err != nil {
		t.Fatalf("migrate isolated database to current head: %v", err)
	}
	if err := migrator.Migrate(75); err != nil {
		t.Fatalf("migrate current head down to 075: %v", err)
	}

	pool, err := pgxpool.New(ctx, migrationDSN)
	if err != nil {
		t.Fatalf("connect to isolated migration database: %v", err)
	}
	defer pool.Close()

	var installationID, repoID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES (1, 'migration-test')
		RETURNING id`).Scan(&installationID); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, 1, 'argus/migration-test')
		RETURNING id`, installationID).Scan(&repoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	legacyCreated := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	insertReviewMarker(t, ctx, pool, repoID, 41, legacyCreated)

	if err := migrator.Migrate(76); err != nil {
		t.Fatalf("migrate 075 up to 076: %v", err)
	}
	assertMarkerCount(t, ctx, pool, repoID, 41, 0)
	assertSignalCount(t, ctx, pool, repoID, 41, 1)

	var migratedCreated, migratedClaimed time.Time
	var migratedDelivered *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT created_at, claimed_at, delivered_at
		FROM review_signals
		WHERE repo_id = $1 AND pr_number = 41 AND kind = 'auto_run_disabled'`, repoID).
		Scan(&migratedCreated, &migratedClaimed, &migratedDelivered); err != nil {
		t.Fatalf("read migrated legacy signal: %v", err)
	}
	if !migratedCreated.Equal(legacyCreated) || !migratedClaimed.Equal(legacyCreated) || migratedDelivered == nil || !migratedDelivered.Equal(legacyCreated) {
		t.Fatalf("migrated legacy timestamps = created %v claimed %v delivered %v, want %v", migratedCreated, migratedClaimed, migratedDelivered, legacyCreated)
	}

	newCreated := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	newClaimed := newCreated.Add(time.Minute)
	if _, err := pool.Exec(ctx, `
		INSERT INTO review_signals (repo_id, pr_number, kind, created_at, claimed_at)
		VALUES ($1, 42, 'auto_run_disabled', $2, $3)`, repoID, newCreated, newClaimed); err != nil {
		t.Fatalf("seed post-up review signal: %v", err)
	}

	// Model a partially restored rollback. The down migration must retain this
	// marker without adding a second row for the matching signal.
	partialCreated := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO review_signals (repo_id, pr_number, kind, created_at, claimed_at, delivered_at)
		VALUES ($1, 43, 'auto_run_disabled', $2, $2, $2)`, repoID, partialCreated); err != nil {
		t.Fatalf("seed partially restored signal: %v", err)
	}
	insertReviewMarker(t, ctx, pool, repoID, 43, partialCreated.Add(-time.Hour))

	if err := migrator.Migrate(75); err != nil {
		t.Fatalf("migrate 076 down to 075: %v", err)
	}
	for _, prNumber := range []int{41, 42, 43} {
		assertMarkerCount(t, ctx, pool, repoID, prNumber, 1)
		found, err := NewWithDB(pool).HasFailedReviewWithError(ctx, repoID, prNumber, "auto_run_disabled")
		if err != nil {
			t.Fatalf("old HasFailedReviewWithError for PR %d: %v", prNumber, err)
		}
		if !found {
			t.Errorf("old HasFailedReviewWithError did not find restored PR %d marker", prNumber)
		}
	}

	var restoredCreated time.Time
	var restoredCompleted *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT created_at, completed_at FROM reviews
		WHERE repo_id = $1 AND pr_number = 41 AND status = 'failed' AND error = 'auto_run_disabled'`, repoID).
		Scan(&restoredCreated, &restoredCompleted); err != nil {
		t.Fatalf("read restored migrated marker: %v", err)
	}
	if !restoredCreated.Equal(legacyCreated) || restoredCompleted == nil || !restoredCompleted.Equal(legacyCreated) {
		t.Fatalf("restored migrated timestamps = created %v completed %v, want %v", restoredCreated, restoredCompleted, legacyCreated)
	}
	if err := pool.QueryRow(ctx, `
		SELECT created_at, completed_at FROM reviews
		WHERE repo_id = $1 AND pr_number = 42 AND status = 'failed' AND error = 'auto_run_disabled'`, repoID).
		Scan(&restoredCreated, &restoredCompleted); err != nil {
		t.Fatalf("read restored post-up marker: %v", err)
	}
	if !restoredCreated.Equal(newCreated) || restoredCompleted != nil {
		t.Fatalf("restored post-up timestamps = created %v completed %v, want created %v and no completion", restoredCreated, restoredCompleted, newCreated)
	}

	// An old binary can race or retry and leave more than one marker because the
	// reviews schema has no marker uniqueness constraint. Re-upgrade must fold
	// all of them back into one signal.
	insertReviewMarker(t, ctx, pool, repoID, 42, newCreated.Add(time.Hour))
	assertMarkerCount(t, ctx, pool, repoID, 42, 2)

	if err := migrator.Migrate(76); err != nil {
		t.Fatalf("re-apply migration 076: %v", err)
	}
	for _, prNumber := range []int{41, 42, 43} {
		assertSignalCount(t, ctx, pool, repoID, prNumber, 1)
		assertMarkerCount(t, ctx, pool, repoID, prNumber, 0)
	}
	if err := pool.QueryRow(ctx, `
		SELECT created_at FROM review_signals
		WHERE repo_id = $1 AND pr_number = 42 AND kind = 'auto_run_disabled'`, repoID).
		Scan(&migratedCreated); err != nil {
		t.Fatalf("read deduplicated signal: %v", err)
	}
	if !migratedCreated.Equal(newCreated) {
		t.Fatalf("deduplicated signal created_at = %v, want earliest marker timestamp %v", migratedCreated, newCreated)
	}
}

func migrationTestDatabaseURL(t *testing.T, baseDSN, databaseName string) string {
	t.Helper()
	u, err := url.Parse(baseDSN)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		t.Fatalf("TEST_DATABASE_URL must be a postgres URL, got scheme %q", u.Scheme)
	}
	u.Path = "/" + databaseName
	return u.String()
}

func insertReviewMarker(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repoID int64, prNumber int, createdAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO reviews
			(repo_id, pr_number, pr_title, pr_author, head_sha, base_sha,
			 status, trigger, error, created_at)
		VALUES ($1, $2, 'synthetic auto-run marker', 'argus', '', '',
			'failed', 'webhook', 'auto_run_disabled', $3)`, repoID, prNumber, createdAt); err != nil {
		t.Fatalf("insert review marker for PR %d: %v", prNumber, err)
	}
}

func assertMarkerCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repoID int64, prNumber, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM reviews
		WHERE repo_id = $1 AND pr_number = $2 AND github_review_id IS NULL
		  AND status = 'failed' AND error = 'auto_run_disabled'`, repoID, prNumber).Scan(&got); err != nil {
		t.Fatalf("count review markers for PR %d: %v", prNumber, err)
	}
	if got != want {
		t.Fatalf("review marker count for PR %d = %d, want %d", prNumber, got, want)
	}
}

func assertSignalCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repoID int64, prNumber, want int) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM review_signals
		WHERE repo_id = $1 AND pr_number = $2 AND kind = 'auto_run_disabled'`, repoID, prNumber).Scan(&got); err != nil {
		t.Fatalf("count review signals for PR %d: %v", prNumber, err)
	}
	if got != want {
		t.Fatalf("review signal count for PR %d = %d, want %d", prNumber, got, want)
	}
}
