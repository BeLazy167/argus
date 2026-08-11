package api

import (
	"context"
	"fmt"
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
	if s.prEventHandler == nil {
		return fmt.Errorf("pull request event handler is unavailable")
	}
	return s.prEventHandler.HandlePREvent(ctx, event)
}

// runPREvent is the synchronous runner used outside Launcher. Reconciliation
// completes before HandlePREvent, which is the first boundary allowed to read
// dismissal memory.
func (s *Server) runPREvent(ctx context.Context, event ghpkg.PREvent) error {
	if err := s.reconcileReactionsBeforeReview(ctx, event.InstallationID, event.RepoFullName, event.PRNumber); err != nil {
		return err
	}
	return s.handlePREventAfterReconciliation(ctx, event)
}

// launchPREvent preserves Launcher's ownership of admission-adjacent slot,
// cancel, semaphore cleanup, and generation behavior. The sweep runs in
// BeforeSpawn, synchronously inside Launch and before path-specific pre-spawn
// work, so a failed reconciliation neither returns an accepted launch nor
// starts a pipeline that could read stale reaction feedback.
func (s *Server) launchPREvent(spec pipeline.LaunchSpec, event ghpkg.PREvent) error {
	pathBeforeSpawn := spec.BeforeSpawn
	spec.BeforeSpawn = func(ctx context.Context) error {
		if err := s.reconcileReactionsBeforeReview(ctx, event.InstallationID, event.RepoFullName, event.PRNumber); err != nil {
			return err
		}
		if pathBeforeSpawn != nil {
			return pathBeforeSpawn(ctx)
		}
		return nil
	}
	spec.Run = func(ctx context.Context) error {
		return s.handlePREventAfterReconciliation(ctx, event)
	}
	return s.launcher.Launch(spec)
}
