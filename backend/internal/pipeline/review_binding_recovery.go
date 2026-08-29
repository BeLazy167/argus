package pipeline

import (
	"context"
	"fmt"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	gh "github.com/google/go-github/v68/github"
	"github.com/google/uuid"
)

// postedReviewBindingClient is the GitHub read seam shared by normal
// post-review binding and crash recovery. Both list methods must enumerate all
// pages before returning success.
type postedReviewBindingClient interface {
	ListReviewComments(context.Context, int64, string, string, int, int64) ([]*gh.PullRequestComment, error)
	ListReviewThreads(context.Context, int64, string, string, int) ([]ghpkg.ReviewThread, error)
}

func (o *Orchestrator) postedReviewBindingClient() postedReviewBindingClient {
	if o.reviewBindingClient != nil {
		return o.reviewBindingClient
	}
	if o.ghClient != nil {
		return o.ghClient
	}
	return nil
}

// RecoverPostedReviewBindings reconstructs the minimum PipelineRun needed to
// bind a marker-verified GitHub review's inline comments and GraphQL threads.
// Repository, installation, pull request, and head identities come only from
// the persisted review graph. The supplied generation and GitHub review ID are
// comparison values, never tenant authority.
//
// Recovery is idempotent. It returns an error until every current-generation,
// posted inline finding has both durable IDs. Callers must not mark the review
// completed or emit terminal events before this method succeeds.
func (o *Orchestrator) RecoverPostedReviewBindings(ctx context.Context, reviewID uuid.UUID, generation int, githubReviewID int64) error {
	if o != nil && o.logger != nil {
		o.logger.InfoContext(ctx, "posted review binding recovery started", "event", "pipeline.binding_recovery.started", "review_id", reviewID, "attempt_generation", generation, "github_review_id", githubReviewID)
	}
	if generation < 1 {
		return fmt.Errorf("recovering posted review bindings: invalid generation %d", generation)
	}
	if githubReviewID <= 0 {
		return fmt.Errorf("recovering posted review bindings: invalid GitHub review id %d", githubReviewID)
	}
	if o == nil || o.st == nil {
		return fmt.Errorf("recovering posted review bindings: store unavailable")
	}

	review, err := o.st.GetReview(ctx, reviewID)
	if err != nil {
		return fmt.Errorf("recovering posted review bindings: loading review: %w", err)
	}
	currentGeneration, err := o.st.GetReviewAttemptGeneration(ctx, reviewID)
	if err != nil {
		return fmt.Errorf("recovering posted review bindings: loading generation: %w", err)
	}
	if currentGeneration != generation {
		o.logger.WarnContext(ctx, "posted review binding recovery rejected for stale generation", "event", "pipeline.binding_recovery.rejected", "review_id", reviewID, "attempt_generation", generation, "current_generation", currentGeneration, "reason", "stale_generation")
		return fmt.Errorf("recovering posted review bindings: generation changed from %d to %d", generation, currentGeneration)
	}
	if review.GithubReviewID == nil || *review.GithubReviewID != githubReviewID {
		return fmt.Errorf("recovering posted review bindings: GitHub review id is not current")
	}
	repo, err := o.st.GetRepo(ctx, review.RepoID)
	if err != nil {
		return fmt.Errorf("recovering posted review bindings: loading repository: %w", err)
	}
	installation, err := o.st.GetInstallation(ctx, repo.InstallationID)
	if err != nil {
		return fmt.Errorf("recovering posted review bindings: loading installation: %w", err)
	}
	owner, repoName, err := splitRepoFullName(repo.FullName)
	if err != nil || owner == "" || repoName == "" {
		return fmt.Errorf("recovering posted review bindings: invalid persisted repository identity %q", repo.FullName)
	}

	run := &PipelineRun{
		ReviewID:          review.ID,
		AttemptGeneration: generation,
		DBRepoID:          repo.ID,
		DBInstallationID:  installation.ID,
		PREvent: ghpkg.PREvent{
			InstallationID: installation.InstallationID,
			RepoID:         repo.GithubID,
			RepoFullName:   repo.FullName,
			PRNumber:       review.PRNumber,
			HeadSHA:        review.HeadSHA,
			BaseSHA:        review.BaseSHA,
		},
	}

	// REST enumeration is always required. Persisted review_comments include
	// findings folded into the summary, so DB rows alone cannot distinguish a
	// summary-only delivery from inline comments that are not visible yet.
	o.logger.InfoContext(ctx, "posted review REST binding recovery started", "event", "pipeline.binding_recovery.rest_started", "review_id", reviewID, "attempt_generation", generation, "github_review_id", githubReviewID)
	if err := o.backfillGitHubCommentIDs(ctx, run, githubReviewID, owner, repoName); err != nil {
		return fmt.Errorf("recovering posted review bindings: %w", err)
	}

	state, err := o.st.GetPostedReviewBindingState(ctx, reviewID, generation, githubReviewID)
	if err != nil {
		return fmt.Errorf("recovering posted review bindings: verifying REST bindings: %w", err)
	}
	if !state.Current {
		return fmt.Errorf("recovering posted review bindings: review delivery changed during REST enumeration")
	}
	o.logger.InfoContext(ctx, "posted review REST binding recovery verified", "event", "pipeline.binding_recovery.rest_verified", "review_id", reviewID, "bound_comment_count", state.BoundGitHubCommentIDs, "missing_thread_count", state.MissingThreadNodeIDs)
	if state.BoundGitHubCommentIDs == 0 {
		o.logger.InfoContext(ctx, "posted review binding recovery completed", "event", "pipeline.binding_recovery.completed", "review_id", reviewID, "attempt_generation", generation, "github_review_id", githubReviewID, "summary_only", true)
		return nil // successful empty enumeration: summary-only review
	}
	if state.MissingThreadNodeIDs > 0 {
		o.logger.InfoContext(ctx, "posted review GraphQL binding recovery started", "event", "pipeline.binding_recovery.graphql_started", "review_id", reviewID, "missing_thread_count", state.MissingThreadNodeIDs)
		if err := o.hydrateThreadNodeIDs(ctx, run, owner, repoName); err != nil {
			return fmt.Errorf("recovering posted review bindings: %w", err)
		}
	}

	state, err = o.st.GetPostedReviewBindingState(ctx, reviewID, generation, githubReviewID)
	if err != nil {
		return fmt.Errorf("recovering posted review bindings: verifying GraphQL bindings: %w", err)
	}
	if !state.Current {
		return fmt.Errorf("recovering posted review bindings: review delivery changed during GraphQL enumeration")
	}
	if state.MissingThreadNodeIDs > 0 {
		o.logger.WarnContext(ctx, "posted review binding recovery incomplete", "event", "pipeline.binding_recovery.incomplete", "review_id", reviewID, "missing_thread_count", state.MissingThreadNodeIDs)
		return fmt.Errorf("recovering posted review bindings: %d delivered comments have no GraphQL thread yet", state.MissingThreadNodeIDs)
	}
	o.logger.InfoContext(ctx, "posted review binding recovery completed", "event", "pipeline.binding_recovery.completed", "review_id", reviewID, "attempt_generation", generation, "github_review_id", githubReviewID, "bound_comment_count", state.BoundGitHubCommentIDs)
	return nil
}
