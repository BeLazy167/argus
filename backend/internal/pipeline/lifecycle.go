// Package pipeline — lifecycle.go is the single home for the review-lifecycle
// DB guards that used to be scattered across orchestrator.go and post():
//
//   - EnsureNotRunning — the retry-vs-live refusal (→ 409).
//   - CancelStranded   — the cross-machine / post-restart stranded cancel.
//   - ShouldAbortPost  — post()'s cancel re-check, consulted before it ships to
//     GitHub.
//
// Each is a compare-and-set or a terminal-skip whose exact semantics are
// load-bearing (a completed review must never flip to cancelled; a fresh live
// run must never be double-run). Collecting them behind one type keeps those
// invariants reviewable in one place and lets them be exercised without a live
// Postgres via the seams wired in NewReviewLifecycle.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrReviewRunning is returned by EnsureNotRunning when a review's pipeline is
// still executing, so a retry would double-run it. Handlers surface it as 409.
var ErrReviewRunning = errors.New("review appears to be running; stop it first or wait")

// lifecycleStore is the narrow store surface ReviewLifecycle needs: a cheap
// status read (ShouldAbortPost) and the compare-and-set status write
// (CancelStranded). *store.Store satisfies it; tests substitute a fake.
type lifecycleStore interface {
	GetReviewStatus(ctx context.Context, id uuid.UUID) (string, error)
	UpdateReviewStatusIf(ctx context.Context, id uuid.UUID, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error)
}

// ReviewLifecycle owns the review-lifecycle DB guards. Construct with
// NewReviewLifecycle; the zero value is not usable.
type ReviewLifecycle struct {
	st       lifecycleStore
	eventBus *EventBus
	logger   *slog.Logger

	// Seams over the pipeline_states row lookups so EnsureNotRunning and
	// CancelStranded can run DB-less in tests. Default-wired to raw SQL /
	// StateMachine methods in NewReviewLifecycle; mirrors StateMachine's
	// isCancelled/persist seam pattern.

	// latestRunLiveness reports the newest persisted run's state and whether it
	// was updated recently enough to look live. found=false ⇔ no persisted run.
	latestRunLiveness func(ctx context.Context, reviewID uuid.UUID) (state PipelineState, fresh, found bool, err error)
	// loadLatestRun loads the newest persisted run for a review. found=false ⇔
	// no persisted run. Errors are already wrapped for the caller.
	loadLatestRun func(ctx context.Context, reviewID uuid.UUID) (run *PipelineRun, found bool, err error)
	// persistRun writes a run's state back (used by CancelStranded to flip a
	// still-running orphan to cancelled).
	persistRun func(ctx context.Context, run *PipelineRun) error
}

// NewReviewLifecycle wires the DB guards over db + store + the state machine.
func NewReviewLifecycle(db *pgxpool.Pool, st lifecycleStore, sm *StateMachine, eventBus *EventBus, logger *slog.Logger) *ReviewLifecycle {
	l := &ReviewLifecycle{st: st, eventBus: eventBus, logger: logger}

	l.latestRunLiveness = func(ctx context.Context, reviewID uuid.UUID) (PipelineState, bool, bool, error) {
		var stateStr string
		var fresh bool
		err := db.QueryRow(ctx,
			`SELECT state, updated_at > NOW() - make_interval(secs => $2)
			 FROM pipeline_states WHERE review_id = $1 ORDER BY updated_at DESC LIMIT 1`,
			reviewID, recoverStaleAfter.Seconds(),
		).Scan(&stateStr, &fresh)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, false, nil
		}
		if err != nil {
			return "", false, false, err
		}
		return PipelineState(stateStr), fresh, true, nil
	}

	l.loadLatestRun = func(ctx context.Context, reviewID uuid.UUID) (*PipelineRun, bool, error) {
		var runID uuid.UUID
		err := db.QueryRow(ctx,
			`SELECT id FROM pipeline_states WHERE review_id = $1 ORDER BY updated_at DESC LIMIT 1`,
			reviewID,
		).Scan(&runID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("finding pipeline run: %w", err)
		}
		run, loadErr := sm.loadState(ctx, runID)
		if loadErr != nil {
			return nil, false, fmt.Errorf("loading run: %w", loadErr)
		}
		return run, true, nil
	}

	l.persistRun = sm.persistState
	return l
}

// shouldRefuseRetry decides whether retrying is unsafe: the latest run is
// non-terminal AND was updated recently enough that a process is likely still
// driving it (possibly on another machine). A stale non-terminal run is treated
// as crashed and is retryable — RecoverIncomplete uses the same window.
func shouldRefuseRetry(state PipelineState, fresh bool) bool {
	return !state.IsTerminal() && fresh
}

// EnsureNotRunning returns ErrReviewRunning when the review's latest pipeline
// run looks live (non-terminal and recently updated). Callers use it to refuse
// a retry that would run concurrently with an in-flight run — notably a
// cancelled-but-not-yet-halted review whose run is still executing/posting.
func (l *ReviewLifecycle) EnsureNotRunning(ctx context.Context, reviewID uuid.UUID) error {
	state, fresh, found, err := l.latestRunLiveness(ctx, reviewID)
	if err != nil {
		return fmt.Errorf("checking live run for review %s: %w", reviewID, err)
	}
	if !found {
		return nil // no run persisted — nothing to collide with
	}
	if shouldRefuseRetry(state, fresh) {
		return ErrReviewRunning
	}
	return nil
}

// CancelStranded marks a review — and its latest pipeline run, if one was
// persisted — cancelled when no in-memory cancel function is available. That
// happens after a process restart, or when the cancel request lands on a
// different Fly machine than the one running the review. Without this the
// review stays pending/in_progress forever and RecoverIncomplete may later
// resurrect the orphaned run.
func (l *ReviewLifecycle) CancelStranded(ctx context.Context, reviewID uuid.UUID) error {
	const note = "stopped by user"

	run, found, err := l.loadLatestRun(ctx, reviewID)
	if err != nil {
		return fmt.Errorf("stranded cancel %s: %w", reviewID, err)
	}
	if found {
		// Skip entirely when the run already reached a terminal state: it kept
		// its real outcome and a completed review must never flip to cancelled.
		if run.State.IsTerminal() {
			return nil
		}
		run.State = StateCancelled
		run.Error = note
		run.UpdatedAt = time.Now()
		if perr := l.persistRun(ctx, run); perr != nil {
			return fmt.Errorf("persisting cancelled run %s: %w", reviewID, perr)
		}
	}
	// No persisted run (review failed before the first state write) falls through
	// here: only the review row needs flipping.

	// Conditional: only cancel a review still pending/in_progress, so we never
	// clobber a completed/failed outcome another writer set. Reporting 200
	// {"cancelled"} to the caller is honest because the running pipeline (this
	// or another machine) consults reviews.status at each stage boundary
	// (StateMachine cooperative cancel) and post() re-checks before posting — so
	// it will actually halt and never post a cancelled review.
	if _, err := l.st.UpdateReviewStatusIf(ctx, reviewID, "cancelled", note, nil, []string{"pending", "in_progress"}); err != nil {
		return fmt.Errorf("updating review status for stranded cancel %s: %w", reviewID, err)
	}

	// Nudge any connected live-stream clients into the stopped state.
	if l.eventBus != nil {
		l.eventBus.Publish(reviewID, EventCancelled, map[string]string{"stage": string(StateCancelled)})
	}
	return nil
}

// ShouldAbortPost reports whether post() must abandon the GitHub submission
// because the review was cancelled — a Stop that landed after the last
// stage-boundary cooperative check. It is the single home for post()'s cancel
// re-checks, called both at entry and immediately before the GitHub post (the
// pre-post enrichment block runs for seconds, widening the cancel window). A
// status-lookup error is logged and treated as "do not abort", matching the
// original inline guards, which logged and proceeded. warnMsg is the caller's
// site-specific failure message — the two sites' historical strings are
// grep-load-bearing (log alerts), so they must not collapse into one.
func (l *ReviewLifecycle) ShouldAbortPost(ctx context.Context, reviewID uuid.UUID, warnMsg string) bool {
	status, err := l.st.GetReviewStatus(ctx, reviewID)
	if err != nil {
		l.logger.Warn(warnMsg, "error", err, "review_id", reviewID)
		return false
	}
	return status == "cancelled"
}
