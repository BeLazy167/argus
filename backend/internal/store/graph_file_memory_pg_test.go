package store

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// The LIMIT-window behaviour of GetFileMemory lives entirely in SQL, so it can
// only be verified against a real Postgres. Gated on TEST_DATABASE_URL —
// deliberately NOT the app's DATABASE_URL — so `go test ./...` on a developer
// machine with live credentials exported can never touch that database.
func fileMemoryTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; PG-backed tests run where the CI harness provides a database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

// seedFileMemoryRepo creates a throwaway installation + repo + review and
// returns the repo id and review id. Each subtest gets its own repo so the
// per-file LIMIT window is never polluted by a sibling case.
func seedFileMemoryRepo(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (int64, string) {
	t.Helper()

	var installID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random() * 1000000000)::bigint, 'file-memory-test')
		RETURNING id`).Scan(&installID)
	if err != nil {
		t.Fatalf("seed installation: %v", err)
	}

	var repoID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, (random() * 1000000000)::bigint, 'acme/file-memory-test')
		RETURNING id`, installID).Scan(&repoID)
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	var reviewID string
	err = pool.QueryRow(ctx, `
		INSERT INTO reviews (repo_id, pr_number, pr_title, pr_author, head_sha, base_sha)
		VALUES ($1, 1, 'seed', 'tester', 'head', 'base')
		RETURNING id::text`, repoID).Scan(&reviewID)
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}

	// reviews -> repos -> installations have no ON DELETE CASCADE, so unwind by
	// hand; review_comments does cascade off reviews.
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM reviews WHERE repo_id = $1`, repoID)
		_, _ = pool.Exec(bg, `DELETE FROM repos WHERE id = $1`, repoID)
		_, _ = pool.Exec(bg, `DELETE FROM installations WHERE id = $1`, installID)
	})

	return repoID, reviewID
}

// seedComment is one review_comments row: `age` places it in the file's
// history (larger = older) and `state` drives the ordering under test.
type seedComment struct {
	body   string
	state  string
	reason *string
	age    time.Duration
}

// TestGetFileMemoryRanksPostedFindingsAheadOfSuppressed proves the File Memory
// sidebar can never lose a file's posted findings to suppression noise. The
// query returns only the 5 most recent comments on a file; suppressed findings
// (generated but never posted to the PR) share that window, so ordering purely
// by created_at let a burst of suppressions push every real finding out of the
// payload — the sidebar then showed a file as having no review history at all.
func TestGetFileMemoryRanksPostedFindingsAheadOfSuppressed(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, Q: db.New(pool)}

	dismissed := "dismissed_match:0.91"
	teamFeedback := "team_feedback:3"

	tests := []struct {
		name string
		seed []seedComment
		// want is the exact body order GetFileMemory must return.
		want []string
	}{
		{
			// The regression that motivated the fix: five fresh suppressions
			// exactly fill the LIMIT window ahead of the one posted finding.
			name: "recent suppression burst never evicts the posted finding",
			seed: []seedComment{
				{body: "posted-old", state: "posted", age: 60 * time.Minute},
				{body: "sup-1", state: "suppressed", reason: &dismissed, age: 5 * time.Minute},
				{body: "sup-2", state: "suppressed", reason: &dismissed, age: 4 * time.Minute},
				{body: "sup-3", state: "suppressed", reason: &dismissed, age: 3 * time.Minute},
				{body: "sup-4", state: "suppressed", reason: &teamFeedback, age: 2 * time.Minute},
				{body: "sup-5", state: "suppressed", reason: &teamFeedback, age: 1 * time.Minute},
			},
			want: []string{"posted-old", "sup-5", "sup-4", "sup-3", "sup-2"},
		},
		{
			// Suppressed rows are an audit record, not garbage: they must still
			// reach the sidebar whenever posted findings leave room.
			name: "suppressed findings still reach the payload, ranked last",
			seed: []seedComment{
				{body: "posted-new", state: "posted", age: 1 * time.Minute},
				{body: "sup-newest", state: "suppressed", reason: &dismissed, age: 30 * time.Second},
			},
			want: []string{"posted-new", "sup-newest"},
		},
		{
			// Non-suppressed lifecycle states were all posted to the PR, so
			// they rank together, newest first — only suppression demotes.
			name: "posted lifecycle states keep newest-first order among themselves",
			seed: []seedComment{
				{body: "addressed-mid", state: "addressed", age: 20 * time.Minute},
				{body: "posted-newest", state: "posted", age: 1 * time.Minute},
				{body: "dismissed-oldest", state: "dismissed", age: 90 * time.Minute},
			},
			want: []string{"posted-newest", "addressed-mid", "dismissed-oldest"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repoID, reviewID := seedFileMemoryRepo(t, ctx, pool)
			const filePath = "internal/pipeline/orchestrator.go"

			now := time.Now().UTC()
			for _, c := range tc.seed {
				_, err := pool.Exec(ctx, `
					INSERT INTO review_comments (review_id, file_path, body, severity, state, suppressed_reason, created_at)
					VALUES ($1, $2, $3, 'warning', $4, $5, $6)`,
					reviewID, filePath, c.body, c.state, c.reason, now.Add(-c.age))
				if err != nil {
					t.Fatalf("seed comment %q: %v", c.body, err)
				}
			}

			mem, err := st.GetFileMemory(ctx, repoID, filePath)
			if err != nil {
				t.Fatalf("GetFileMemory: %v", err)
			}

			got := make([]string, 0, len(mem.RecentComments))
			for _, c := range mem.RecentComments {
				got = append(got, c.Body)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("sidebar would show the wrong findings for this file:\n got %v\nwant %v", got, tc.want)
			}

			// The sidebar distinguishes suppressed rows by state and explains
			// them with the reason; both must survive the round trip or a
			// never-posted finding reads as one Argus actually posted.
			for _, c := range mem.RecentComments {
				if c.State == "suppressed" && (c.SuppressedReason == nil || *c.SuppressedReason == "") {
					t.Errorf("suppressed finding %q lost its reason: the sidebar badge would have nothing to explain the suppression", c.Body)
				}
				if c.State != "suppressed" && c.SuppressedReason != nil {
					t.Errorf("posted finding %q carries a suppression reason %q: the sidebar would mute a finding that was really posted", c.Body, *c.SuppressedReason)
				}
			}
		})
	}
}
