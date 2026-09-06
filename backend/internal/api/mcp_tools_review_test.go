package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BeLazy167/argus/backend/internal/store"
)

func seedReview(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repoID int64, pr int, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO reviews (repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, status)
		VALUES ($1, $2, 'seed title', 'someone', 'head', 'base', $3) RETURNING id`, repoID, pr, status).Scan(&id); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM pipeline_states WHERE review_id = $1`, id)
		_, _ = pool.Exec(bg, `DELETE FROM review_comments WHERE review_id = $1`, id)
		_, _ = pool.Exec(bg, `DELETE FROM reviews WHERE id = $1`, id)
	})
	return id
}

func newReviewFixture(t *testing.T) (context.Context, *pgxpool.Pool, *Server, int64, int64) {
	t.Helper()
	pool, ctx := architectureTestPool(t)
	installID, repoID := seedArchitectureRepo(t, ctx, pool)
	s := &Server{store: store.NewWithDB(pool), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return ctx, pool, s, installID, repoID
}

func TestListReviewsScopedAndClamped(t *testing.T) {
	ctx, pool, s, installID, repoID := newReviewFixture(t)
	_, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	mine := seedReview(t, ctx, pool, repoID, 1, "completed")
	theirs := seedReview(t, ctx, pool, foreignRepo, 2, "completed")
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, out, err := tools.listReviews(ctx, nil, listReviewsInput{Limit: 1000, Offset: -5})
	if err != nil {
		t.Fatal(err)
	}
	var sawMine, sawTheirs bool
	for _, r := range out.Reviews {
		sawMine = sawMine || r.ReviewID == mine.String()
		sawTheirs = sawTheirs || r.ReviewID == theirs.String()
	}
	if !sawMine || sawTheirs {
		t.Fatalf("sawMine=%v sawTheirs=%v — the unfiltered listing must exclude other installations", sawMine, sawTheirs)
	}
	if _, _, err := tools.listReviews(ctx, nil, listReviewsInput{RepoID: foreignRepo}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign repo_id: %v", err)
	}
	noRead := &mcpTools{srv: s, scope: tenantScope{installationIDs: []int64{installID}, grantedScopes: []string{scopeMemoryWrite}}}
	if _, _, err := noRead.listReviews(ctx, nil, listReviewsInput{}); err == nil || err.Error() != errInsufficientScope(scopeRead).Error() {
		t.Fatalf("missing read scope: %v", err)
	}
}

func TestGetReviewStatusIncludesStageAndGuardsTenant(t *testing.T) {
	ctx, pool, s, installID, repoID := newReviewFixture(t)
	id := seedReview(t, ctx, pool, repoID, 7, "in_progress")
	if _, err := pool.Exec(ctx, `INSERT INTO pipeline_states (review_id, state) VALUES ($1, 'synthesizing')`, id); err != nil {
		t.Fatal(err)
	}
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, out, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "in_progress" || out.Stage != "synthesizing" || out.PRNumber != 7 || out.RepoFullName == "" || out.GitHubPRURL == "" {
		t.Fatalf("out = %+v", out)
	}
	noRun := seedReview(t, ctx, pool, repoID, 8, "pending")
	if _, out, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: noRun.String()}); err != nil || out.Stage != "" {
		t.Fatalf("no-run review must omit stage, never guess it from status: out=%+v err=%v", out, err)
	}
	_, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	foreign := seedReview(t, ctx, pool, foreignRepo, 9, "completed")
	if _, _, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: foreign.String()}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign review: %v", err)
	}
	if _, _, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: uuid.New().String()}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("missing review must match foreign: %v", err)
	}
	if _, _, err := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: "not-a-uuid"}); err == nil || errors.Is(err, errNotAccessible) {
		t.Fatalf("malformed id should be a validation error, got %v", err)
	}
}

// Severity values here are "critical"/"warning", not the brief's "high"/"low":
// review_comments_severity_check on the live schema only admits
// critical|warning|suggestion|praise, so "high"/"low" would violate the CHECK
// constraint on insert. Structure and assertions are otherwise unchanged.
func TestGetReviewPartitionsSuppressedAndCounts(t *testing.T) {
	ctx, pool, s, installID, repoID := newReviewFixture(t)
	id := seedReview(t, ctx, pool, repoID, 11, "completed")
	insert := func(path, sev, state string) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO review_comments (review_id, file_path, body, severity, state, attempt_generation)
			VALUES ($1, $2, 'body', $3, $4, 1)`, id, path, sev, state); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
	}
	insert("a.go", "critical", "posted")
	insert("a.go", "warning", "posted")
	insert("b.go", "critical", "suppressed")
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	_, raw, err := tools.getReview(ctx, nil, getReviewInput{ReviewID: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	out, ok := raw.(getReviewOutput)
	if !ok {
		t.Fatalf("output type = %T", raw)
	}
	if len(out.Findings) != 2 || len(out.SuppressedFindings) != 0 {
		t.Fatalf("default: findings=%d suppressed=%d, want 2/0", len(out.Findings), len(out.SuppressedFindings))
	}
	if out.Counts.Total != 2 || out.Counts.BySeverity["critical"] != 1 || out.Counts.BySeverity["warning"] != 1 || out.Counts.ByFile["a.go"] != 2 {
		t.Fatalf("counts = %+v — must describe returned (non-suppressed) findings only", out.Counts)
	}
	if out.RepoFullName == "" || out.GitHubPRURL == "" || out.Review == nil || out.Review.ID != id || out.DegradedSections == nil {
		t.Fatalf("out = %+v", out)
	}
	_, raw, err = tools.getReview(ctx, nil, getReviewInput{ReviewID: id.String(), IncludeSuppressed: true})
	if err != nil {
		t.Fatal(err)
	}
	out = raw.(getReviewOutput)
	if len(out.SuppressedFindings) != 1 || out.SuppressedFindings[0].FilePath != "b.go" || out.Counts.Total != 2 {
		t.Fatalf("include_suppressed: %+v", out)
	}

	_, foreignRepo := seedArchitectureRepo(t, ctx, pool)
	foreign := seedReview(t, ctx, pool, foreignRepo, 12, "completed")
	if _, _, err := tools.getReview(ctx, nil, getReviewInput{ReviewID: foreign.String()}); !errors.Is(err, errNotAccessible) {
		t.Fatalf("foreign review: %v", err)
	}
	noRead := &mcpTools{srv: s, scope: tenantScope{installationIDs: []int64{installID}, grantedScopes: []string{scopeMemoryWrite}}}
	if _, _, err := noRead.getReview(ctx, nil, getReviewInput{ReviewID: id.String()}); err == nil || err.Error() != errInsufficientScope(scopeRead).Error() {
		t.Fatalf("missing read scope: %v", err)
	}
}

func TestGitHubPRURL(t *testing.T) {
	t.Parallel()
	if got := githubPRURL("acme/widgets", 42, nil); got != "https://github.com/acme/widgets/pull/42" {
		t.Fatalf("got %q", got)
	}
	rid := int64(99)
	if got := githubPRURL("acme/widgets", 42, &rid); got != "https://github.com/acme/widgets/pull/42#pullrequestreview-99" {
		t.Fatalf("got %q", got)
	}
}

// errSidecarDown stands in for any store failure on a degradable read.
var errSidecarDown = errors.New("sidecar down")

// failingSidecarStore delegates every degradable read to the real store except
// the one named in fail, which errors. Failing exactly one at a time is what
// proves degraded_sections names the section that actually broke and leaves the
// other three intact; asserting the field is non-nil on the happy path proves
// only that it was initialised.
type failingSidecarStore struct {
	reviewSidecarSource
	fail string
}

func (f failingSidecarStore) ListPRReviewSummaries(ctx context.Context, repoID int64, prNumber int) ([]store.PRReviewSummary, error) {
	if f.fail == "history" {
		return nil, errSidecarDown
	}
	return f.reviewSidecarSource.ListPRReviewSummaries(ctx, repoID, prNumber)
}

func (f failingSidecarStore) ListPRAutoResolveEvents(ctx context.Context, repoID int64, prNumber int) ([]store.AutoResolveSummary, error) {
	if f.fail == "auto_resolve_events" {
		return nil, errSidecarDown
	}
	return f.reviewSidecarSource.ListPRAutoResolveEvents(ctx, repoID, prNumber)
}

func (f failingSidecarStore) ListReviewMemories(ctx context.Context, installationID int64, reviewID uuid.UUID, limit int) ([]store.LearnedMemory, error) {
	if f.fail == "memories" {
		return nil, errSidecarDown
	}
	return f.reviewSidecarSource.ListReviewMemories(ctx, installationID, reviewID, limit)
}

func (f failingSidecarStore) CountReviewMemoriesByType(ctx context.Context, installationID int64, reviewID uuid.UUID) ([]store.LearnedMemoryCount, error) {
	if f.fail == "memory_counts" {
		return nil, errSidecarDown
	}
	return f.reviewSidecarSource.CountReviewMemoriesByType(ctx, installationID, reviewID)
}

// seedReviewSidecars gives all four degradable sections at least one row, so a
// section that comes back empty in the tests below is empty because it failed,
// not because nothing was ever there. History needs no seed: it reads the
// reviews table, and the review itself is the row.
func seedReviewSidecars(t *testing.T, ctx context.Context, pool *pgxpool.Pool, installID, repoID int64, prNumber int, reviewID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO auto_resolve_events (installation_id, repo_id, pr_number, source_sha, resolved_count, attempted_count)
		VALUES ($1, $2, $3, 'sha-degraded', 1, 1)`, installID, repoID, prNumber); err != nil {
		t.Fatalf("seed auto-resolve event: %v", err)
	}
	var memoryID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO memories (installation_id, container_tag, custom_id, type, content, metadata)
		VALUES ($1, 'x', $2, 'pattern', 'remembered thing', '{"source":"manual"}'::jsonb)
		RETURNING id`, installID, "degraded-"+reviewID.String()).Scan(&memoryID); err != nil {
		t.Fatalf("seed memory: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memory_review_attributions (memory_id, review_id) VALUES ($1, $2)`, memoryID, reviewID); err != nil {
		t.Fatalf("seed memory attribution: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memories WHERE id = $1`, memoryID)
		_, _ = pool.Exec(bg, `DELETE FROM auto_resolve_events WHERE repo_id = $1`, repoID)
	})
}

// Hazard 16: a degraded section must read as degraded, not as "nothing here".
func TestGetReviewDegradedSectionsNameTheFailedRead(t *testing.T) {
	ctx, pool, s, installID, repoID := newReviewFixture(t)
	const prNumber = 21
	id := seedReview(t, ctx, pool, repoID, prNumber, "completed")
	seedReviewSidecars(t, ctx, pool, installID, repoID, prNumber, id)
	tools := &mcpTools{srv: s, scope: readScope(installID)}

	run := func(t *testing.T) getReviewOutput {
		t.Helper()
		_, raw, err := tools.getReview(ctx, nil, getReviewInput{ReviewID: id.String()})
		if err != nil {
			t.Fatalf("get_review: %v", err)
		}
		out, ok := raw.(getReviewOutput)
		if !ok {
			t.Fatalf("output type = %T", raw)
		}
		return out
	}
	// Control: with every read healthy, no section is named and all four carry
	// rows. Without this the per-section assertions below could pass on a
	// fixture that never populated anything.
	healthy := run(t)
	if len(healthy.DegradedSections) != 0 {
		t.Fatalf("healthy read named %v", healthy.DegradedSections)
	}
	if len(healthy.History) == 0 || len(healthy.AutoResolveEvents) == 0 || len(healthy.Memories) == 0 || len(healthy.MemoryCounts) == 0 {
		t.Fatalf("fixture did not populate all four sections: history=%d auto=%d mem=%d counts=%d",
			len(healthy.History), len(healthy.AutoResolveEvents), len(healthy.Memories), len(healthy.MemoryCounts))
	}

	sections := []struct {
		name    string
		lengths func(getReviewOutput) (failed int, failedNil bool, others []int)
	}{
		{"history", func(o getReviewOutput) (int, bool, []int) {
			return len(o.History), o.History == nil, []int{len(o.AutoResolveEvents), len(o.Memories), len(o.MemoryCounts)}
		}},
		{"auto_resolve_events", func(o getReviewOutput) (int, bool, []int) {
			return len(o.AutoResolveEvents), o.AutoResolveEvents == nil, []int{len(o.History), len(o.Memories), len(o.MemoryCounts)}
		}},
		{"memories", func(o getReviewOutput) (int, bool, []int) {
			return len(o.Memories), o.Memories == nil, []int{len(o.History), len(o.AutoResolveEvents), len(o.MemoryCounts)}
		}},
		{"memory_counts", func(o getReviewOutput) (int, bool, []int) {
			return len(o.MemoryCounts), o.MemoryCounts == nil, []int{len(o.History), len(o.AutoResolveEvents), len(o.Memories)}
		}},
	}
	for _, sec := range sections {
		t.Run(sec.name, func(t *testing.T) {
			s.reviewSidecarStore = failingSidecarStore{reviewSidecarSource: s.store, fail: sec.name}
			t.Cleanup(func() { s.reviewSidecarStore = nil })
			out := run(t)
			if !slices.Contains(out.DegradedSections, sec.name) {
				t.Fatalf("degraded_sections = %v, want it to name %q", out.DegradedSections, sec.name)
			}
			if len(out.DegradedSections) != 1 {
				t.Fatalf("degraded_sections = %v, want only the failed read", out.DegradedSections)
			}
			failedLen, failedNil, others := sec.lengths(out)
			if failedNil || failedLen != 0 {
				t.Fatalf("%s: nil=%v len=%d — a failed section must serialize as [] , never null", sec.name, failedNil, failedLen)
			}
			for i, n := range others {
				if n == 0 {
					t.Fatalf("%s failed but sibling section %d came back empty: %+v", sec.name, i, out)
				}
			}
		})
	}
}
