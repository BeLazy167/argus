package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	storemigrations "github.com/BeLazy167/argus/backend/internal/store/migrations"
)

// TestPatternsCustomUniqDriftMigration reproduces the production shape that
// migration 088 exists to repair and proves the repair through golang-migrate
// itself — the same path the Fly release command takes. CI migrates an empty
// database, where every statement in 088 is a no-op, so without this test the
// only path that matters is never exercised.
//
// Production ran a variant of 086 whose unique index carried an extra
// `source = 'convention'` predicate. CreatePattern's ON CONFLICT arbiter could
// not be inferred from it (SQLSTATE 42P10), so every pattern write failed for
// six days. The drifted database also held one duplicate identity pair, and
// review_comments.matched_pattern_id — a NO ACTION foreign key — may point at
// either row, because the identity lookup that populates it uses an unordered
// LIMIT 1.
func TestPatternsCustomUniqDriftMigration(t *testing.T) {
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

	// Stop at 086, the migration production drifted from, so the drift can be
	// installed before 088 runs.
	if err := migrator.Migrate(86); err != nil {
		t.Fatalf("migrate isolated database to 086: %v", err)
	}

	pool, err := pgxpool.New(ctx, migrationDSN)
	if err != nil {
		t.Fatalf("connect to isolated migration database: %v", err)
	}
	defer pool.Close()
	st := NewWithDB(pool)

	// Install the exact production drift: the index 086 should have created,
	// replaced by the narrower one production actually got.
	if _, err := pool.Exec(ctx, `
		DROP INDEX patterns_installation_memory_custom_uniq;
		CREATE UNIQUE INDEX patterns_convention_memory_custom_uniq
		  ON patterns (installation_id, memory_custom_id)
		  WHERE memory_custom_id IS NOT NULL AND source = 'convention'`); err != nil {
		t.Fatalf("install production index drift: %v", err)
	}

	var installationID, repoID int64
	var reviewID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES (1, 'drift-test')
		RETURNING id`).Scan(&installationID); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, 1, 'argus/drift-test')
		RETURNING id`, installationID).Scan(&repoID); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO reviews (repo_id, pr_number, pr_title, pr_author, head_sha, base_sha)
		VALUES ($1, 1, 'seed', 'tester', 'head', 'base')
		RETURNING id::text`, repoID).Scan(&reviewID); err != nil {
		t.Fatalf("seed review: %v", err)
	}

	// The duplicate identity pair, inserted directly: CreatePattern itself is
	// broken on this shape, which is the point.
	const identity = "drift-test--confirmed--4476f7d71ce2"
	seedDuplicate := func() int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO patterns (installation_id, repo_id, content, source, memory_custom_id)
			VALUES ($1, $2, 'duplicated pattern', 'scoring_confirmed', $3)
			RETURNING id`, installationID, repoID, identity).Scan(&id); err != nil {
			t.Fatalf("seed duplicate pattern: %v", err)
		}
		return id
	}
	lowID := seedDuplicate()
	highID := seedDuplicate()

	// A finding attributed to the sibling the migration will delete.
	var commentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO review_comments (review_id, file_path, body, severity, state, matched_pattern_id)
		VALUES ($1::uuid, 'a.go', 'finding', 'warning', 'posted', $2)
		RETURNING id::text`, reviewID, highID).Scan(&commentID); err != nil {
		t.Fatalf("seed review comment: %v", err)
	}

	// The outage reproduces: the real upsert cannot infer its arbiter index.
	manualSource := "manual"
	probeID := "drift-test--manual--probe"
	_, err = st.CreatePattern(ctx, installationID, &repoID, "probe", nil, nil, &manualSource, nil, nil, &probeID, nil)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42P10" {
		t.Fatalf("drifted shape: CreatePattern must fail with SQLSTATE 42P10, got %v", err)
	}

	// Apply 088 through golang-migrate, which sets the dirty flag before running
	// and clears it only on success. A clean 88 afterwards is the whole claim.
	if err := migrator.Migrate(88); err != nil {
		t.Fatalf("migrate 086 up to 088 on the drifted shape: %v", err)
	}
	version, dirty, err := migrator.Version()
	if err != nil || version != 88 || dirty {
		t.Fatalf("after 088: version=%d dirty=%v err=%v, want 88 clean", version, dirty, err)
	}

	// The finding follows the surviving lowest sibling and the duplicate is gone.
	var matched int64
	if err := pool.QueryRow(ctx, `SELECT matched_pattern_id FROM review_comments WHERE id = $1::uuid`, commentID).Scan(&matched); err != nil {
		t.Fatalf("read repointed comment: %v", err)
	}
	if matched != lowID {
		t.Fatalf("matched_pattern_id = %d, want surviving sibling %d (deleted %d)", matched, lowID, highID)
	}
	var survivors int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM patterns WHERE installation_id = $1 AND memory_custom_id = $2`, installationID, identity).Scan(&survivors); err != nil {
		t.Fatalf("count survivors: %v", err)
	}
	if survivors != 1 {
		t.Fatalf("identity %q has %d rows after 088, want 1", identity, survivors)
	}
	var lowRemains bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM patterns WHERE id = $1)`, lowID).Scan(&lowRemains); err != nil {
		t.Fatalf("check surviving row: %v", err)
	}
	if !lowRemains {
		t.Fatalf("088 deleted the lowest id %d instead of keeping it", lowID)
	}

	// The index shapes are reconciled to what 086 intended.
	assertPatternsIndex(t, ctx, pool, "patterns_installation_memory_custom_uniq", true)
	assertPatternsIndex(t, ctx, pool, "patterns_convention_memory_custom_uniq", false)

	// And the write path is back.
	if _, err := st.CreatePattern(ctx, installationID, &repoID, "probe", nil, nil, &manualSource, nil, nil, &probeID, nil); err != nil {
		t.Fatalf("CreatePattern after 088: %v", err)
	}
}

func assertPatternsIndex(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string, want bool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes WHERE tablename = 'patterns' AND indexname = $1
		)`, name).Scan(&exists); err != nil {
		t.Fatalf("look up index %s: %v", name, err)
	}
	if exists != want {
		t.Fatalf("index %s exists=%v, want %v", name, exists, want)
	}
}
