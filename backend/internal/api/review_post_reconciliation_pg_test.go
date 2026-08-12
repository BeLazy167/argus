package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
)

type postLookupStub struct {
	id                        int64
	found                     bool
	err                       error
	installationID            int64
	owner, repo, marker, head string
	pr                        int
}

func (f *postLookupStub) FindReviewByMarker(_ context.Context, installationID int64, owner, repo string, pr int, marker, head string) (int64, bool, error) {
	f.installationID, f.owner, f.repo, f.pr, f.marker, f.head = installationID, owner, repo, pr, marker, head
	return f.id, f.found, f.err
}

func TestReconcileAmbiguousReviewPost(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	dbInstallationID, repoID := seedArchitectureRepo(t, ctx, pool)
	const githubInstallationID int64 = 880071
	reviewID := uuid.New()
	generation := 4
	oldClaim := store.ErrReviewPostPersistenceAmbiguous.Error() + ": posting authority claimed; review=" + reviewID.String() + "; generation=4; claimed_at=" + time.Now().Add(-store.ReviewPostReconciliationMinAge-time.Minute).UTC().Format(time.RFC3339Nano) + "; reconciliation required"
	if _, err := pool.Exec(ctx, `
		INSERT INTO reviews(id,repo_id,pr_number,pr_title,pr_author,head_sha,base_sha,status,error,attempt_generation)
		VALUES($1,$2,19,'title','author','head-19','base','failed',$3,$4)
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
		srv := &Server{store: store.NewWithDB(pool), reviewPostLookup: lookup, logger: logger}
		if err := srv.reconcileAmbiguousReviewPost(ctx, review, repo, githubInstallationID, "user-1"); err != nil {
			t.Fatal(err)
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
		if _, err := pool.Exec(ctx, `UPDATE reviews SET github_review_id=NULL,error=$2 WHERE id=$1`, reviewID, oldClaim); err != nil {
			t.Fatal(err)
		}
		lookup := &postLookupStub{}
		srv := &Server{store: store.NewWithDB(pool), reviewPostLookup: lookup, logger: logger}
		if err := srv.reconcileAmbiguousReviewPost(ctx, review, repo, githubInstallationID, "user-1"); err != nil {
			t.Fatal(err)
		}
		var stateError *string
		if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&stateError); err != nil || stateError != nil {
			t.Fatalf("error=%v query=%v", stateError, err)
		}
	})

	t.Run("eventual consistency window never guesses absence", func(t *testing.T) {
		recentClaim := store.ErrReviewPostPersistenceAmbiguous.Error() + ": posting authority claimed; review=" + reviewID.String() + "; generation=4; claimed_at=" + time.Now().UTC().Format(time.RFC3339Nano) + "; reconciliation required"
		if _, err := pool.Exec(ctx, `UPDATE reviews SET github_review_id=NULL,error=$2 WHERE id=$1`, reviewID, recentClaim); err != nil {
			t.Fatal(err)
		}
		recentReview := *review
		recentReview.Error = &recentClaim
		srv := &Server{store: store.NewWithDB(pool), reviewPostLookup: &postLookupStub{}, logger: logger}
		if err := srv.reconcileAmbiguousReviewPost(ctx, &recentReview, repo, githubInstallationID, "user-1"); !errors.Is(err, errReviewPostRetryLater) {
			t.Fatalf("error=%v", err)
		}
		var kept string
		if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&kept); err != nil || kept != recentClaim {
			t.Fatalf("claim=%q query=%v", kept, err)
		}
	})

	t.Run("lookup failure remains blocked", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE reviews SET error=$2 WHERE id=$1`, reviewID, oldClaim); err != nil {
			t.Fatal(err)
		}
		srv := &Server{store: store.NewWithDB(pool), reviewPostLookup: &postLookupStub{err: errors.New("GitHub unavailable")}, logger: logger}
		if err := srv.reconcileAmbiguousReviewPost(ctx, review, repo, githubInstallationID, "user-1"); !errors.Is(err, errReviewPostRetryLater) {
			t.Fatalf("error=%v", err)
		}
		var kept string
		if err := pool.QueryRow(ctx, `SELECT error FROM reviews WHERE id=$1`, reviewID).Scan(&kept); err != nil || kept != oldClaim {
			t.Fatalf("claim=%q query=%v", kept, err)
		}
	})
}
