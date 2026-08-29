package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store/db"
)

// countSeed is one review_comments row for the suppression-count tests. endLine
// is distinct per row because ListArchBugDensity dedups bugs by
// (pr_number, end_line) — sharing a line would collapse rows and hide a
// miscount behind the dedup instead of the state predicate.
type countSeed struct {
	body     string
	severity string
	state    string
	endLine  int
	category string
	isNew    bool
	patScore float64
}

// seedCountRepo creates a throwaway installation + repo + completed review and
// returns (installationRowID, repoID, reviewID). Distinct from
// seedFileMemoryRepo because these tests need the installation row id (org stats
// scope by it) and a 'completed' status (StatsOverview filters on it).
func seedCountRepo(t *testing.T, ctx context.Context, pool *pgxpool.Pool, author string) (int64, int64, string) {
	t.Helper()

	var installID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random() * 1000000000)::bigint, 'suppressed-count-test')
		RETURNING id`).Scan(&installID)
	if err != nil {
		t.Fatalf("seed installation: %v", err)
	}

	var repoID int64
	err = pool.QueryRow(ctx, `
		INSERT INTO repos (installation_id, github_id, full_name)
		VALUES ($1, (random() * 1000000000)::bigint, 'acme/suppressed-count-test')
		RETURNING id`, installID).Scan(&repoID)
	if err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	var reviewID string
	err = pool.QueryRow(ctx, `
		INSERT INTO reviews (repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, status, score, github_review_id, completed_at)
		VALUES ($1, 1, 'seed', $2, 'head', 'base', 'completed', 7, 42, NOW())
		RETURNING id::text`, repoID, author).Scan(&reviewID)
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

	return installID, repoID, reviewID
}

func insertCountSeeds(t *testing.T, ctx context.Context, pool *pgxpool.Pool, reviewID, filePath string, seeds []countSeed) {
	t.Helper()
	for _, s := range seeds {
		var patScore *float64
		if s.patScore > 0 {
			v := s.patScore
			patScore = &v
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO review_comments (review_id, file_path, end_line, body, severity, category, state, is_new_finding, matched_pattern_score)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			reviewID, filePath, s.endLine, s.body, s.severity, s.category, s.state, s.isNew, patScore)
		if err != nil {
			t.Fatalf("seed comment %q: %v", s.body, err)
		}
	}
}

// TestArchBugDensityExcludesSuppressedFindings pins the #239 defect: bug density
// and the risk score derived from it counted findings the suppression pass had
// already withheld, so a file whose critical findings were ALL suppressed still
// rendered a nonzero bug_density, a raised risk score, and could be labelled
// "Bug hotspot. High defect rate per line." — a defect claim about findings no
// PR author ever received.
//
// The `prs` assertions are as load-bearing as the `bugs` ones. `prs` is change
// frequency, not a defect claim: it must keep counting a PR whose findings on
// the file were all suppressed. Asserting the file is still PRESENT with prs=1
// is what rejects the tempting-but-wrong fix of moving the predicate into the
// WHERE clause, which drops all-suppressed files from the result entirely.
func TestArchBugDensityExcludesSuppressedFindings(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}

	const filePath = "internal/pipeline/orchestrator.go"

	tests := []struct {
		name string
		seed []countSeed
		// wantBugs is ListArchBugDensity's deduped per-file bug count.
		wantBugs int
		// wantPRs is the change-frequency count, which suppression must not touch.
		wantPRs int
		// wantFileBugs is GetFileBugCount's undeduped single-file count.
		wantFileBugs int
	}{
		{
			// The headline case from #239. Before the fix this returned bugs=3.
			name: "every critical on the file was suppressed",
			seed: []countSeed{
				{body: "sup-a", severity: "critical", state: "suppressed", endLine: 10},
				{body: "sup-b", severity: "critical", state: "suppressed", endLine: 20},
				{body: "sup-c", severity: "warning", state: "suppressed", endLine: 30},
			},
			wantBugs:     0,
			wantPRs:      1,
			wantFileBugs: 0,
		},
		{
			name: "suppressed findings do not pad a real bug count",
			seed: []countSeed{
				{body: "posted", severity: "critical", state: "posted", endLine: 10},
				{body: "sup-a", severity: "critical", state: "suppressed", endLine: 20},
				{body: "sup-b", severity: "critical", state: "suppressed", endLine: 30},
				{body: "sup-c", severity: "warning", state: "suppressed", endLine: 40},
			},
			wantBugs:     1,
			wantPRs:      1,
			wantFileBugs: 1,
		},
		{
			// Only 'suppressed' means never-posted. Every other lifecycle state
			// reached the PR, so all of them stay countable defects — narrowing
			// to state = 'posted' would erase a file's whole history the moment
			// its findings were addressed.
			name: "every non-suppressed lifecycle state still counts",
			seed: []countSeed{
				{body: "posted", severity: "critical", state: "posted", endLine: 10},
				{body: "addressed", severity: "critical", state: "addressed", endLine: 20},
				{body: "dismissed", severity: "warning", state: "dismissed", endLine: 30},
				{body: "deferred", severity: "warning", state: "deferred", endLine: 40},
				{body: "resolved", severity: "critical", state: "resolved", endLine: 50},
			},
			wantBugs:     5,
			wantPRs:      1,
			wantFileBugs: 5,
		},
		{
			// Guards the other half of the predicate: adding the state filter
			// must not loosen the severity filter that was already there.
			name: "suggestion and praise severities stay excluded",
			seed: []countSeed{
				{body: "posted", severity: "critical", state: "posted", endLine: 10},
				{body: "nit", severity: "suggestion", state: "posted", endLine: 20},
				{body: "nice", severity: "praise", state: "posted", endLine: 30},
			},
			wantBugs:     1,
			wantPRs:      1,
			wantFileBugs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, repoID, reviewID := seedCountRepo(t, ctx, pool, "tester")
			insertCountSeeds(t, ctx, pool, reviewID, filePath, tc.seed)

			rows, err := st.ListArchBugDensity(ctx, repoID)
			if err != nil {
				t.Fatalf("ListArchBugDensity: %v", err)
			}

			var got *db.ListArchBugDensityRow
			for i := range rows {
				if rows[i].FilePath == filePath {
					got = &rows[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("file %q missing from bug density: the architecture view would lose the file entirely, "+
					"and with it its change frequency — the state predicate belongs in the bugs FILTER, not the WHERE", filePath)
			}
			if got.Bugs != tc.wantBugs {
				t.Errorf("bug count feeding bug_density, risk score and the \"Bug hotspot\" label: got %d, want %d "+
					"(suppressed findings were never posted and must not read as defects)", got.Bugs, tc.wantBugs)
			}
			if got.Prs != tc.wantPRs {
				t.Errorf("change frequency: got %d PRs, want %d — a PR whose findings were all suppressed still changed this file", got.Prs, tc.wantPRs)
			}

			fileBugs, err := st.q.GetFileBugCount(ctx, db.GetFileBugCountParams{RepoID: repoID, FilePath: filePath})
			if err != nil {
				t.Fatalf("GetFileBugCount: %v", err)
			}
			if fileBugs != tc.wantFileBugs {
				t.Errorf("single-file bug count injected into the review prompt: got %d, want %d "+
					"(it tells the model \"N bugs have been found in this file\")", fileBugs, tc.wantFileBugs)
			}
		})
	}
}

// TestFindingAggregatesExcludeObsoleteAttempts prevents a retry's hidden
// findings from inflating architecture and organization statistics.
func TestFindingAggregatesExcludeObsoleteAttempts(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := NewWithDB(pool)
	installID, repoID, reviewID := seedCountRepo(t, ctx, pool, "retry-metrics")
	const filePath = "retry.go"
	if _, err := pool.Exec(ctx, `INSERT INTO review_comments (review_id, attempt_generation, file_path, end_line, body, severity, state)
		VALUES ($1, 1, $2, 10, 'obsolete critical', 'critical', 'posted')`, reviewID, filePath); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE reviews SET attempt_generation = 2 WHERE id = $1`, reviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO review_comments (review_id, attempt_generation, file_path, end_line, body, severity, state)
		VALUES ($1, 2, $2, 20, 'current warning', 'warning', 'posted')`, reviewID, filePath); err != nil {
		t.Fatal(err)
	}

	rows, err := st.ListArchBugDensity(ctx, repoID)
	if err != nil || len(rows) != 1 || rows[0].Bugs != 1 || rows[0].Prs != 1 {
		t.Fatalf("architecture rows=%+v err=%v, want one current-attempt bug", rows, err)
	}
	fileBugs, err := st.q.GetFileBugCount(ctx, db.GetFileBugCountParams{RepoID: repoID, FilePath: filePath})
	if err != nil || fileBugs != 1 {
		t.Fatalf("file bugs=%d err=%v, want current-attempt count 1", fileBugs, err)
	}
	stats, err := st.GetStatsScoped(ctx, []int64{installID})
	if err != nil || stats.CriticalFinds != 0 {
		t.Fatalf("scoped criticals=%d err=%v, obsolete critical leaked", stats.CriticalFinds, err)
	}
	period := pgtype.Interval{Days: 30, Valid: true}
	overview, err := st.q.StatsOverview(ctx, db.StatsOverviewParams{InstallationIds: []int64{installID}, Period: period})
	if err != nil || overview.CriticalFinds != 0 {
		t.Fatalf("overview criticals=%d err=%v, obsolete critical leaked", overview.CriticalFinds, err)
	}
	severity, err := st.q.StatsFindingsBySeverity(ctx, db.StatsFindingsBySeverityParams{InstallationIds: []int64{installID}, Period: period})
	if err != nil || len(severity) != 1 || severity[0].Severity == nil || *severity[0].Severity != "warning" || severity[0].Count != 1 {
		t.Fatalf("severity rows=%+v err=%v, want only current warning", severity, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO comment_outcomes (review_comment_id, outcome)
		SELECT id, 'confirmed' FROM review_comments WHERE review_id=$1`, reviewID); err != nil {
		t.Fatal(err)
	}
	learn, err := st.q.GetLearnLayerCounts(ctx, db.GetLearnLayerCountsParams{InstallationIds: []int64{installID}, Period: "30 days"})
	if err != nil || learn.FeedbackIndexed != 1 {
		t.Fatalf("feedback indexed=%d err=%v, want only current-attempt outcome", learn.FeedbackIndexed, err)
	}
}

// TestStatsCountsExcludeSuppressedFindings covers the siblings of the #239
// query: every dashboard count that aggregates review_comments. They make the
// same claim — "this is what Argus reported" — and a suppressed row is exactly
// what Argus decided not to report. Each is asserted here because the defect
// this issue follows up on was a correct fix applied to one of a pair.
func TestStatsCountsExcludeSuppressedFindings(t *testing.T) {
	pool, ctx := fileMemoryTestPool(t)
	st := &Store{Pool: pool, q: db.New(pool)}
	q := db.New(pool)

	const author = "suppression-count-author"
	const filePath = "internal/api/handlers.go"

	// GetStats is unscoped (it counts the whole table), so it can only be
	// asserted as a delta around this test's own seed.
	before, err := st.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats baseline: %v", err)
	}

	installID, _, reviewID := seedCountRepo(t, ctx, pool, author)
	insertCountSeeds(t, ctx, pool, reviewID, filePath, []countSeed{
		{body: "crit-posted-1", severity: "critical", state: "posted", endLine: 10, category: "bug", isNew: true},
		{body: "crit-posted-2", severity: "critical", state: "addressed", endLine: 20, category: "bug", isNew: true},
		{body: "crit-sup-1", severity: "critical", state: "suppressed", endLine: 30, category: "bug", isNew: true},
		{body: "crit-sup-2", severity: "critical", state: "suppressed", endLine: 40, category: "bug", isNew: true},
		{body: "crit-sup-3", severity: "critical", state: "suppressed", endLine: 50, category: "security", patScore: 0.9},
		{body: "warn-posted", severity: "warning", state: "posted", endLine: 60, category: "security", patScore: 0.8},
		{body: "warn-sup", severity: "warning", state: "suppressed", endLine: 70, category: "security", patScore: 0.7},
	})

	instIDs := []int64{installID}
	period := pgtype.Interval{Days: 30, Valid: true}

	t.Run("GetStats critical_finds", func(t *testing.T) {
		after, err := st.GetStats(ctx)
		if err != nil {
			t.Fatalf("GetStats: %v", err)
		}
		if delta := after.CriticalFinds - before.CriticalFinds; delta != 2 {
			t.Errorf("global critical_finds grew by %d, want 2 — the 3 suppressed criticals were never shown to anyone", delta)
		}
	})

	t.Run("GetStatsScoped critical_finds", func(t *testing.T) {
		got, err := st.GetStatsScoped(ctx, instIDs)
		if err != nil {
			t.Fatalf("GetStatsScoped: %v", err)
		}
		if got.CriticalFinds != 2 {
			t.Errorf("scoped critical_finds = %d, want 2", got.CriticalFinds)
		}
	})

	t.Run("StatsOverview critical_finds", func(t *testing.T) {
		row, err := q.StatsOverview(ctx, db.StatsOverviewParams{InstallationIds: instIDs, Period: period})
		if err != nil {
			t.Fatalf("StatsOverview: %v", err)
		}
		if row.CriticalFinds != 2 {
			t.Errorf("org overview critical_finds = %d, want 2", row.CriticalFinds)
		}
	})

	t.Run("StatsUserCriticals", func(t *testing.T) {
		rows, err := q.StatsUserCriticals(ctx, db.StatsUserCriticalsParams{InstallationIds: instIDs, Period: period})
		if err != nil {
			t.Fatalf("StatsUserCriticals: %v", err)
		}
		if len(rows) != 1 || rows[0].PRAuthor != author {
			t.Fatalf("expected exactly one row for %q, got %+v", author, rows)
		}
		if rows[0].CriticalCount != 2 {
			t.Errorf("per-author critical_count = %d, want 2 — charging an author for findings never shown to them", rows[0].CriticalCount)
		}
	})

	t.Run("StatsFindingsBySeverity", func(t *testing.T) {
		rows, err := q.StatsFindingsBySeverity(ctx, db.StatsFindingsBySeverityParams{InstallationIds: instIDs, Period: period})
		if err != nil {
			t.Fatalf("StatsFindingsBySeverity: %v", err)
		}
		got := map[string]int{}
		for _, r := range rows {
			if r.Severity != nil {
				got[*r.Severity] = r.Count
			}
		}
		want := map[string]int{"critical": 2, "warning": 1}
		for sev, n := range want {
			if got[sev] != n {
				t.Errorf("severity %q = %d, want %d (full breakdown %v)", sev, got[sev], n, got)
			}
		}
	})

	t.Run("StatsFindingsByCategory", func(t *testing.T) {
		rows, err := q.StatsFindingsByCategory(ctx, db.StatsFindingsByCategoryParams{InstallationIds: instIDs, Period: period})
		if err != nil {
			t.Fatalf("StatsFindingsByCategory: %v", err)
		}
		got := map[string]int{}
		for _, r := range rows {
			if r.Category != nil {
				got[*r.Category] = r.Count
			}
		}
		want := map[string]int{"bug": 2, "security": 1}
		for cat, n := range want {
			if got[cat] != n {
				t.Errorf("category %q = %d, want %d (full breakdown %v)", cat, got[cat], n, got)
			}
		}
	})

	t.Run("StatsFindingsNewVsPattern", func(t *testing.T) {
		row, err := q.StatsFindingsNewVsPattern(ctx, db.StatsFindingsNewVsPatternParams{InstallationIds: instIDs, Period: period})
		if err != nil {
			t.Fatalf("StatsFindingsNewVsPattern: %v", err)
		}
		if row.NewFindings != 2 {
			t.Errorf("new_findings = %d, want 2 — suppressed rows carry is_new_finding too", row.NewFindings)
		}
		if row.PatternMatches != 1 {
			t.Errorf("pattern_matches = %d, want 1 — a suppressed pattern match never proved memory useful to anyone", row.PatternMatches)
		}
	})
}
