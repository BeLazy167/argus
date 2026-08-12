package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/store"
)

var errReviewPostRetryLater = errors.New("review post reconciliation is pending")

type reviewPostReconciliationOutcome string

const (
	reviewPostNotNeeded        reviewPostReconciliationOutcome = "not_needed"
	reviewPostClaimCleared     reviewPostReconciliationOutcome = "claim_cleared"
	reviewPostAlreadyDelivered reviewPostReconciliationOutcome = "already_delivered"
)

// reconcileAmbiguousReviewPost is the dashboard retry safety gate. It uses the
// authenticated review's own repository and installation; no scope supplied by
// the request is trusted for the GitHub lookup.
func (s *Server) reconcileAmbiguousReviewPost(ctx context.Context, review *store.Review, repo *store.Repo, githubInstallationID int64, userID string) (reviewPostReconciliationOutcome, error) {
	if review.Error == nil || !strings.HasPrefix(*review.Error, store.ErrReviewPostPersistenceAmbiguous.Error()) {
		return reviewPostNotNeeded, nil
	}
	if s.reviewPostLookup == nil {
		return reviewPostNotNeeded, fmt.Errorf("%w: review lookup is unavailable", errReviewPostRetryLater)
	}
	owner, repoName, ok := strings.Cut(repo.FullName, "/")
	if !ok || owner == "" || repoName == "" {
		return reviewPostNotNeeded, fmt.Errorf("%w: malformed repository identity", errReviewPostRetryLater)
	}
	generation, err := s.store.GetReviewAttemptGeneration(ctx, review.ID)
	if err != nil {
		return reviewPostNotNeeded, fmt.Errorf("%w: reading attempt generation: %v", errReviewPostRetryLater, err)
	}
	exactClaim := *review.Error
	marker := ghpkg.ReviewMarker(review.ID.String(), generation)
	lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	githubReviewID, found, lookupErr := s.reviewPostLookup.FindReviewByMarker(
		lookupCtx, githubInstallationID, owner, repoName, review.PRNumber, marker, review.HeadSHA,
	)
	cancel()
	if lookupErr != nil {
		return reviewPostNotNeeded, fmt.Errorf("%w: remote lookup failed: %v", errReviewPostRetryLater, lookupErr)
	}

	action := "post_claim_cleared"
	outcome := reviewPostClaimCleared
	if found {
		attached, attachErr := s.store.AttachReconciledReviewID(ctx, review.ID, generation, exactClaim, githubReviewID)
		if attachErr != nil || !attached {
			return reviewPostNotNeeded, fmt.Errorf("%w: attaching verified review id: %v", errReviewPostRetryLater, attachErr)
		}
		recoverer := s.reviewBindingRecoverer
		if recoverer == nil {
			recoverer = s.orchestrator
		}
		if recoverer == nil {
			return reviewPostNotNeeded, fmt.Errorf("%w: review binding recovery is unavailable", errReviewPostRetryLater)
		}
		if recoverErr := recoverer.RecoverPostedReviewBindings(ctx, review.ID, generation, githubReviewID); recoverErr != nil {
			return reviewPostNotNeeded, fmt.Errorf("%w: recovering delivered review bindings: %v", errReviewPostRetryLater, recoverErr)
		}
		completion, completeErr := s.store.CompleteReconciledReviewWithEvents(ctx, review.ID, generation, exactClaim, githubReviewID, store.ReconciledCompletionMetadata{RepoID: repo.ID, PRNumber: review.PRNumber, InstallationID: githubInstallationID})
		if completeErr != nil || completion == store.ReviewCompletionRejected {
			return reviewPostNotNeeded, fmt.Errorf("%w: completing verified review id: %v", errReviewPostRetryLater, completeErr)
		}
		outcome = reviewPostAlreadyDelivered
		if completion == store.ReviewCompletionAlreadyCompleted {
			return outcome, nil
		}
		action = "post_id_attached"
	} else {
		cleared, clearErr := s.store.ClearReconciledReviewClaim(ctx, review.ID, generation, exactClaim)
		if clearErr != nil || !cleared {
			return reviewPostNotNeeded, fmt.Errorf("%w: claim is recent or changed: %v", errReviewPostRetryLater, clearErr)
		}
	}

	s.logger.InfoContext(ctx, "review post reconciled",
		"event", "review.post_reconciled",
		"action", action,
		"review_id", review.ID,
		"repo", repo.FullName,
		"pr_number", review.PRNumber,
		"installation_id", githubInstallationID,
		"attempt_generation", generation,
		"github_review_id", githubReviewID,
	)
	metadata, _ := json.Marshal(map[string]any{
		"review_id": review.ID.String(), "action": action, "attempt_generation": generation,
	})
	installationID := repo.InstallationID
	if err := s.store.LogActivity(ctx, &installationID, "review."+action, userID, repo.FullName, metadata); err != nil {
		s.logger.Warn("review post reconciliation audit write failed", "error", err, "review_id", review.ID)
	}
	return outcome, nil
}
