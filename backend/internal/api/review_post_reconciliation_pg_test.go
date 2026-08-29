package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/config"
	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/inflight"
	"github.com/BeLazy167/argus/backend/internal/pipeline"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type postLookupStub struct {
	mu                        sync.Mutex
	id                        int64
	found                     bool
	err                       error
	installationID            int64
	owner, repo, marker, head string
	pr                        int
}

func (f *postLookupStub) FindReviewByMarker(_ context.Context, installationID int64, owner, repo string, pr int, marker, head string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installationID, f.owner, f.repo, f.pr, f.marker, f.head = installationID, owner, repo, pr, marker, head
	return f.id, f.found, f.err
}

func TestReconcileAmbiguousReviewPost(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	dbInstallationID, repoID := seedArchitectureRepo(t, ctx, pool)
	const githubInstallationID int64 = 880071
	reviewID := uuid.New()
	generation := 4
	oldClaim := store.ErrReviewPostPersistenceAmbiguous.Error() + ": posting authority claimed; review=" + reviewID.String() + "; generation=4; claimed_at=" + time.Now().Add(24*time.Hour).UTC().Format(time.RFC3339Nano) + "; reconciliation required"
	if _, err := pool.Exec(ctx, `
		INSERT INTO reviews(id,repo_id,pr_number,pr_title,pr_author,head_sha,base_sha,status,error,attempt_generation,review_post_claimed_at)
		VALUES($1,$2,19,'title','author','head-19','base','failed',$3,$4,NOW()-INTERVAL '1 hour')
	`, reviewID, repoID, oldClaim, generation); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reviews WHERE id=$1`, reviewID) })
	var fullName string
	if err := pool.QueryRow(ctx, `SELECT full_name FROM repos WHERE id=$1`, repoID).Scan(&fullName); err != nil {
		t.Fatal(err)
	}
	review := &store.Review{ID: reviewID, RepoID: repoID, PRNumber: 19, HeadSHA: "head-19", Status: "failed", Error: &oldClaim}
	repo := &store.Repo{ID: repoID, InstallationID: dbInstallationID, FullName: fullName}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("committed response loss attaches exact remote id with tenant scope", func(t *testing.T) {
		lookup := &postLookupStub{id: 9911, found: true}
		srv := &Server{store: store.NewWithDB(pool), reviewPostLookup: lookup, reviewBindingRecoverer: &reviewBindingRecovererStub{}, logger: logger}
		outcome, err := srv.reconcileAmbiguousReviewPost(ctx, review, repo, githubInstallationID, "user-1")
		if err != nil || outcome != reviewPostAlreadyDelivered {
			t.Fatalf("outcome=%q err=%v", outcome, err)
		}
		if lookup.installationID != githubInstallationID || lookup.owner+"/"+lookup.repo != fullName || lookup.pr != 19 || lookup.head != "head-19" {
			t.Fatalf("lookup scope = installation %d repo %s/%s pr %d head %s", lookup.installationID, lookup.owner, lookup.repo, lookup.pr, lookup.head)
		}
		if lookup.marker != ghpkg.ReviewMarker(reviewID.String(), generation) {
			t.Fatalf("marker = %q", lookup.marker)
		}
		var id int64
		if err := pool.QueryRow(ctx, `SELECT github_review_id FROM reviews WHERE id=$1`, reviewID).Scan(&id); err != nil || id != 9911 {
			t.Fatalf("stored id=%d err=%v", id, err)
		}
	})

	t.Run("stale pre-call crash with complete absence clears claim", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE reviews SET status='failed',github_review_id=NULL,error=$2,review_post_claimed_at=NOW()-INTERVAL '1 hour' WHERE id=$1`, reviewID, oldClaim); err != nil {
			t.Fatal(err)
		}
		lookup := &postLookupStub{}
		srv := &Server{store: store.NewWithDB(pool), reviewPostLookup: lookup, logger: logger}
		outcome, err := srv.reconcileAmbiguousReviewPost(ctx, review, repo, githubInstallationID, "user-1")
		if err != nil || outcome != reviewPostClaimCleared {
			t.Fatalf("outcome=%q err=%v", outcome, err)
		}
		var stateError *string
		if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&stateError); err != nil || stateError != nil {
			t.Fatalf("error=%v query=%v", stateError, err)
		}
	})

	t.Run("eventual consistency window never guesses absence", func(t *testing.T) {
		recentClaim := store.ErrReviewPostPersistenceAmbiguous.Error() + ": posting authority claimed; review=" + reviewID.String() + "; generation=4; claimed_at=" + time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339Nano) + "; reconciliation required"
		if _, err := pool.Exec(ctx, `UPDATE reviews SET status='failed',github_review_id=NULL,error=$2,review_post_claimed_at=NOW() WHERE id=$1`, reviewID, recentClaim); err != nil {
			t.Fatal(err)
		}
		recentReview := *review
		recentReview.Error = &recentClaim
		srv := &Server{store: store.NewWithDB(pool), reviewPostLookup: &postLookupStub{}, logger: logger}
		if _, err := srv.reconcileAmbiguousReviewPost(ctx, &recentReview, repo, githubInstallationID, "user-1"); !errors.Is(err, errReviewPostRetryLater) {
			t.Fatalf("error=%v", err)
		}
		var kept string
		if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&kept); err != nil || kept != recentClaim {
			t.Fatalf("claim=%q query=%v", kept, err)
		}
	})

	t.Run("lookup failure remains blocked", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE reviews SET status='failed',error=$2,review_post_claimed_at=NOW()-INTERVAL '1 hour' WHERE id=$1`, reviewID, oldClaim); err != nil {
			t.Fatal(err)
		}
		srv := &Server{store: store.NewWithDB(pool), reviewPostLookup: &postLookupStub{err: errors.New("GitHub unavailable")}, logger: logger}
		if _, err := srv.reconcileAmbiguousReviewPost(ctx, review, repo, githubInstallationID, "user-1"); !errors.Is(err, errReviewPostRetryLater) {
			t.Fatalf("error=%v", err)
		}
		var kept string
		if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&kept); err != nil || kept != oldClaim {
			t.Fatalf("claim=%q query=%v", kept, err)
		}
	})
}

type reviewRetryRunnerStub struct {
	ensureCalls atomic.Int32
	retryCalls  chan int
}

func (r *reviewRetryRunnerStub) EnsureNotRunning(context.Context, uuid.UUID) error {
	r.ensureCalls.Add(1)
	return nil
}

func (r *reviewRetryRunnerStub) RetryReview(_ context.Context, _ uuid.UUID, generation int) error {
	r.retryCalls <- generation
	return context.Canceled
}

type reviewBindingRecovererStub struct {
	mu       sync.Mutex
	failures int
	calls    int
}

func (r *reviewBindingRecovererStub) RecoverPostedReviewBindings(context.Context, uuid.UUID, int, int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.failures > 0 {
		r.failures--
		return errors.New("binding enumeration unavailable")
	}
	return nil
}

func retryReviewRequest(ctx context.Context, reviewID uuid.UUID) *http.Request {
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("reviewID", reviewID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, routeCtx)
	ctx = context.WithValue(ctx, userIDKey, "retry-user")
	return httptest.NewRequest(http.MethodPost, "/api/v1/reviews/"+reviewID.String()+"/retry", nil).WithContext(ctx)
}

func TestRetryReviewReconciliationFoundShortCircuitsAndAbsenceLaunches(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	dbInstallationID, repoID := seedArchitectureRepo(t, ctx, pool)
	ctx = context.WithValue(ctx, installationIDsKey, []int64{dbInstallationID})
	var githubInstallationID int64
	var fullName string
	if err := pool.QueryRow(ctx, `
		SELECT i.installation_id,r.full_name
		FROM repos r JOIN installations i ON i.id=r.installation_id
		WHERE r.id=$1
	`, repoID).Scan(&githubInstallationID, &fullName); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("positive marker completes existing generation without launch", func(t *testing.T) {
		reviewID := uuid.New()
		claim := store.ErrReviewPostPersistenceAmbiguous.Error() + ": marker timestamp may be skewed"
		if _, err := pool.Exec(ctx, `
			INSERT INTO reviews(id,repo_id,pr_number,pr_title,pr_author,head_sha,base_sha,status,error,attempt_generation,review_post_claimed_at)
			VALUES($1,$2,41,'title','author','head-41','base','failed',$3,4,NOW())
		`, reviewID, repoID, claim); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO review_comments(review_id,attempt_generation,file_path,end_line,body)
			VALUES($1,4,'delivered.go',9,'original delivered comment')
		`, reviewID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reviews WHERE id=$1`, reviewID) })

		eventBus := pipeline.NewEventBus()
		var reviewCompletedEvents atomic.Int32
		var postedEvents atomic.Int32
		var terminalEvents atomic.Int32
		eventBus.SubscribeGlobal(func(gotID uuid.UUID, evt pipeline.Event) {
			if gotID != reviewID {
				return
			}
			switch evt.Type {
			case pipeline.EventReviewCompleted:
				reviewCompletedEvents.Add(1)
			case pipeline.EventPostedToGitHub:
				postedEvents.Add(1)
			case pipeline.EventCompleted:
				terminalEvents.Add(1)
			}
		})
		recoverer := &reviewBindingRecovererStub{failures: 1}
		srv := &Server{
			store:                  store.NewWithDB(pool),
			reviewPostLookup:       &postLookupStub{id: 90041, found: true},
			reviewBindingRecoverer: recoverer,
			eventBus:               eventBus,
			logger:                 logger,
			// Deliberately nil: reaching the normal retry precheck or launcher is
			// a test failure by panic. Positive reconciliation must return first.
			reviewRetrier: nil,
			launcher:      nil,
		}
		first := httptest.NewRecorder()
		srv.retryReview(first, retryReviewRequest(ctx, reviewID))
		if first.Code != http.StatusConflict {
			t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
		}
		var firstStoredID int64
		var firstStatus string
		if err := pool.QueryRow(ctx, `SELECT github_review_id,status FROM reviews WHERE id=$1`, reviewID).Scan(&firstStoredID, &firstStatus); err != nil {
			t.Fatal(err)
		}
		if firstStoredID != 90041 || firstStatus != "failed" {
			t.Fatalf("after failed binding: id=%d status=%q", firstStoredID, firstStatus)
		}
		if reviewCompletedEvents.Load() != 0 || postedEvents.Load() != 0 || terminalEvents.Load() != 0 {
			t.Fatalf("failed binding emitted events: review=%d posted=%d terminal=%d", reviewCompletedEvents.Load(), postedEvents.Load(), terminalEvents.Load())
		}

		rr := httptest.NewRecorder()
		srv.retryReview(rr, retryReviewRequest(ctx, reviewID))
		if rr.Code != http.StatusOK {
			t.Fatalf("retry status=%d body=%s", rr.Code, rr.Body.String())
		}
		var body map[string]string
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["status"] != "already delivered" || body["review_id"] != reviewID.String() {
			t.Fatalf("body=%v", body)
		}

		var storedID int64
		var status string
		var generation int
		if err := pool.QueryRow(ctx, `SELECT github_review_id,status,attempt_generation FROM reviews WHERE id=$1`, reviewID).Scan(&storedID, &status, &generation); err != nil {
			t.Fatal(err)
		}
		if storedID != 90041 || status != "completed" || generation != 4 {
			t.Fatalf("review=id %d status %q generation %d", storedID, status, generation)
		}
		comments, err := srv.store.GetReviewComments(ctx, reviewID)
		if err != nil {
			t.Fatal(err)
		}
		if len(comments) != 1 || comments[0].Body != "original delivered comment" {
			t.Fatalf("current comments=%+v", comments)
		}
		if got := reviewCompletedEvents.Load(); got != 0 {
			t.Fatalf("ephemeral review_completed events=%d, want 0", got)
		}
		if got := postedEvents.Load(); got != 0 {
			t.Fatalf("ephemeral posted_to_github events=%d, want 0", got)
		}
		if got := terminalEvents.Load(); got != 0 {
			t.Fatalf("ephemeral completed events=%d, want 0", got)
		}
		var audits int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM activity_log
			WHERE action='review.post_id_attached' AND actor='retry-user' AND resource=$1
			  AND metadata->>'review_id'=$2
		`, fullName, reviewID.String()).Scan(&audits); err != nil {
			t.Fatal(err)
		}
		if audits != 1 {
			t.Fatalf("reconciliation audits=%d, want 1", audits)
		}
	})

	t.Run("concurrent positive recovery emits one terminal lifecycle", func(t *testing.T) {
		reviewID := uuid.New()
		claim := store.ErrReviewPostPersistenceAmbiguous.Error() + ": concurrent recovery"
		if _, err := pool.Exec(ctx, `
			INSERT INTO reviews(id,repo_id,pr_number,pr_title,pr_author,head_sha,base_sha,status,error,attempt_generation,review_post_claimed_at)
			VALUES($1,$2,43,'title','author','head-43','base','failed',$3,6,NOW())
		`, reviewID, repoID, claim); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reviews WHERE id=$1`, reviewID) })
		review, err := store.NewWithDB(pool).GetReview(ctx, reviewID)
		if err != nil {
			t.Fatal(err)
		}
		repoRow, err := store.NewWithDB(pool).GetRepo(ctx, repoID)
		if err != nil {
			t.Fatal(err)
		}
		eventBus := pipeline.NewEventBus()
		var terminalEvents atomic.Int32
		var postedEvents atomic.Int32
		eventBus.SubscribeGlobal(func(gotID uuid.UUID, evt pipeline.Event) {
			if gotID != reviewID {
				return
			}
			if evt.Type == pipeline.EventCompleted {
				terminalEvents.Add(1)
			}
			if evt.Type == pipeline.EventPostedToGitHub {
				postedEvents.Add(1)
			}
		})
		srv := &Server{
			store:                  store.NewWithDB(pool),
			reviewPostLookup:       &postLookupStub{id: 90043, found: true},
			reviewBindingRecoverer: &reviewBindingRecovererStub{},
			eventBus:               eventBus,
			logger:                 logger,
		}
		var wg sync.WaitGroup
		var failures atomic.Int32
		for range 12 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				outcome, err := srv.reconcileAmbiguousReviewPost(context.Background(), review, repoRow, githubInstallationID, "race-user")
				if err != nil || outcome != reviewPostAlreadyDelivered {
					failures.Add(1)
				}
			}()
		}
		wg.Wait()
		if failures.Load() != 0 {
			t.Fatalf("concurrent recovery failures=%d", failures.Load())
		}
		if terminalEvents.Load() != 0 || postedEvents.Load() != 0 {
			t.Fatalf("events posted=%d terminal=%d, want one each", postedEvents.Load(), terminalEvents.Load())
		}
	})

	t.Run("negative aged lookup advances and launches next generation", func(t *testing.T) {
		reviewID := uuid.New()
		claim := store.ErrReviewPostPersistenceAmbiguous.Error() + ": marker timestamp is not authority"
		if _, err := pool.Exec(ctx, `
			INSERT INTO reviews(id,repo_id,pr_number,pr_title,pr_author,head_sha,base_sha,status,error,attempt_generation,review_post_claimed_at)
			VALUES($1,$2,42,'title','author','head-42','base','failed',$3,4,NOW()-INTERVAL '1 hour')
		`, reviewID, repoID, claim); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reviews WHERE id=$1`, reviewID) })

		runner := &reviewRetryRunnerStub{retryCalls: make(chan int, 1)}
		st := store.NewWithDB(pool)
		eventBus := pipeline.NewEventBus()
		srv := &Server{
			store:            st,
			reviewPostLookup: &postLookupStub{},
			reviewRetrier:    runner,
			eventBus:         eventBus,
			logger:           logger,
			rateLimiter:      NewRateLimiter(),
			cfg:              &config.Config{GitHubAppSlug: "argus-eye"},
		}
		srv.inflight = inflight.NewRegistry()
		srv.launcher = pipeline.NewLauncher(srv.inflight, eventBus, st, logger)

		rr := httptest.NewRecorder()
		srv.retryReview(rr, retryReviewRequest(ctx, reviewID))
		if rr.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
		}
		select {
		case generation := <-runner.retryCalls:
			if generation != 5 {
				t.Fatalf("launched generation=%d, want 5", generation)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("retry pipeline was not launched")
		}
		if runner.ensureCalls.Load() != 1 {
			t.Fatalf("EnsureNotRunning calls=%d, want 1", runner.ensureCalls.Load())
		}
		var generation int
		if err := pool.QueryRow(ctx, `SELECT attempt_generation FROM reviews WHERE id=$1`, reviewID).Scan(&generation); err != nil {
			t.Fatal(err)
		}
		if generation != 5 {
			t.Fatalf("stored generation=%d, want 5", generation)
		}
	})
}
