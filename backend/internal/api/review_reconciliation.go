package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/pipeline"
)

// reactionSweeper is the pre-review reconciliation boundary. Production wires
// *pipeline.ReactionAnalyzer; the narrow seam makes the ordering invariant
// directly testable without a GitHub installation.
type reactionSweeper interface {
	SweepPRReactions(context.Context, int64, string, int) error
}

// prEventHandler is the pipeline boundary that may retrieve dismissal memory.
// It stays separate from Server.orchestrator so the rest of the orchestrator's
// API does not need a broad test interface.
type prEventHandler interface {
	HandlePREvent(context.Context, ghpkg.PREvent) error
}

const preReviewReactionSweepTimeout = 60 * time.Second

// reconcileReactionsBeforeReview makes reaction feedback current before any
// pipeline stage can retrieve it. Failure is fail-closed for this review: using
// an old reaction-owned dismissal would silently trust a reaction that may no
// longer exist. If memory is not wired, suppression is unavailable anyway and
// there is nothing to reconcile.
func (s *Server) reconcileReactionsBeforeReview(ctx context.Context, installationID int64, repoFullName string, prNumber int) error {
	op := s.beginOperation(ctx, "review.reconcileReactionsBeforeReview")
	defer op.Complete()
	if s.reactionSweeper == nil {
		if s.memRegistry != nil {
			return fmt.Errorf("reaction reconciliation is unavailable while review memory is enabled")
		}
		return nil
	}

	sweepCtx, cancel := context.WithTimeout(ctx, preReviewReactionSweepTimeout)
	defer cancel()
	if err := s.reactionSweeper.SweepPRReactions(sweepCtx, installationID, repoFullName, prNumber); err != nil {
		return fmt.Errorf("reconciling reactions before review: %w", err)
	}
	return nil
}

// handlePREventAfterReconciliation is the only boundary that enters the
// pipeline after the reaction sweep has succeeded.
func (s *Server) handlePREventAfterReconciliation(ctx context.Context, event ghpkg.PREvent) error {
	op := s.beginOperation(ctx, "review.handlePREventAfterReconciliation")
	defer op.Complete()
	if s.prEventHandler == nil {
		return fmt.Errorf("pull request event handler is unavailable")
	}
	return s.prEventHandler.HandlePREvent(ctx, event)
}

// runPREvent is the synchronous runner used outside Launcher. Reconciliation
// completes before HandlePREvent, which is the first boundary allowed to read
// dismissal memory.
func (s *Server) runPREvent(ctx context.Context, event ghpkg.PREvent) error {
	op := s.beginOperation(ctx, "review.runPREvent")
	defer op.Complete()
	if err := s.reconcileReactionsBeforeReview(ctx, event.InstallationID, event.RepoFullName, event.PRNumber); err != nil {
		return err
	}
	return s.handlePREventAfterReconciliation(ctx, event)
}

// launchPREvent preserves Launcher's ownership of admission-adjacent slot,
// cancel, semaphore cleanup, and generation behavior. Path-specific admission
// runs first so an unauthorized request cannot force a full reaction sweep.
// Reconciliation remains a synchronous barrier before the pipeline can read
// dismissal memory.
func (s *Server) launchPREvent(spec pipeline.LaunchSpec, event ghpkg.PREvent) error {
	pathBeforeSpawn := spec.BeforeSpawn
	spec.BeforeSpawn = func(ctx context.Context) error {
		pathPrepared := false
		if pathBeforeSpawn != nil {
			if err := pathBeforeSpawn(ctx); err != nil {
				// Existing path hooks roll back any partial acquisition before they
				// return an error. Calling Cleanup here would double-release them.
				return err
			}
			pathPrepared = true
		}
		if err := s.reconcileReactionsBeforeReview(ctx, event.InstallationID, event.RepoFullName, event.PRNumber); err != nil {
			// A successful path hook transferred its acquired resource to
			// Cleanup, but Launcher only invokes Cleanup after spawning. Release
			// it here because reconciliation prevented the spawn.
			if pathPrepared && spec.Cleanup != nil {
				spec.Cleanup()
			}
			return err
		}
		return nil
	}
	spec.Run = func(ctx context.Context) error {
		return s.handlePREventAfterReconciliation(ctx, event)
	}
	return s.launcher.Launch(spec)
}

// writeReviewLaunchUnavailable maps an unclassified synchronous launch error
// to a retryable, sanitized response. Callers handle their path sentinels first.
// Returning true lets HTTP handlers return before writing success/activity.
func writeReviewLaunchUnavailable(w http.ResponseWriter, launchErr error) bool {
	if launchErr == nil {
		return false
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "review could not start"})
	return true
}

// restoreCheckboxAfterSynchronousLaunchFailure writes the exact UI body that
// preceded the checked edit when a synchronous failure happens after the
// Running swap. It invokes update at most once; failures before the swap do not
// need an update.
func restoreCheckboxAfterSynchronousLaunchFailure(
	launchErr error,
	runningBody, checkedBody, previousBody string,
	update func(string) error,
) (bool, error) {
	if launchErr == nil || runningBody == "" || runningBody == checkedBody {
		return false, nil
	}
	if previousBody == "" {
		previousBody = pipeline.ResetTriggerCheckbox(checkedBody)
	}
	if previousBody == runningBody {
		return false, nil
	}
	return true, update(previousBody)
}
