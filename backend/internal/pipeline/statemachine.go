package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StateMachine drives a PipelineRun through stages, persisting state to Postgres.
type StateMachine struct {
	db       *pgxpool.Pool
	stages   map[PipelineState]StageFunc
	eventBus *EventBus
	logger   *slog.Logger

	// isCancelled reports whether the review was flagged cancelled in the DB —
	// by a Stop request on this or another machine, or after a restart. It is
	// consulted at every stage boundary (see Run) so a DB-only cancel halts the
	// pipeline instead of running to completion and posting to GitHub. Wired to
	// a reviews.status lookup in NewStateMachine; overridable in tests.
	isCancelled func(ctx context.Context, reviewID uuid.UUID) (bool, error)
	// onTerminal fires after a run is persisted as failed or cancelled, so a
	// caller can reflect that outcome outside the database — today, rewriting
	// the PR's "watch live" progress comment. Nil is a no-op, which is what
	// tests and any embedder that posts nothing want. It is deliberately NOT
	// called on success: that path minimizes the comment itself, in-process,
	// where it still holds the node id.
	onTerminal func(ctx context.Context, reviewID uuid.UUID, outcome StartedOutcome, detail string)
	// persist and setStatus wrap the two Postgres mutations the stage loop
	// performs, and load wraps the one read Resume performs, so Run and Resume
	// can be exercised without a live DB. Defaults wired in NewStateMachine.
	// setStatus is a compare-and-set: an empty allowedCurrent means an
	// unconditional write.
	persist           func(ctx context.Context, run *PipelineRun) error
	setStatus         func(ctx context.Context, reviewID uuid.UUID, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error)
	setAttemptStatus  func(ctx context.Context, reviewID uuid.UUID, generation int, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error)
	currentGeneration func(ctx context.Context, reviewID uuid.UUID) (int, error)
	load              func(ctx context.Context, runID uuid.UUID) (*PipelineRun, error)
	// hydrate re-resolves the context a persisted run loses to json:"-" (feature
	// flags, similarity thresholds, memory indexer, review contract) before the
	// loaded run re-enters the stage loop. Nil is a no-op, which is what tests
	// and any embedder without a store want; NewOrchestrator wires it to
	// Orchestrator.hydrateResumedRun. See resume_context.go.
	hydrate func(ctx context.Context, run *PipelineRun)
}

func NewStateMachine(db *pgxpool.Pool, st *store.Store, logger *slog.Logger) *StateMachine {
	sm := &StateMachine{
		db:     db,
		stages: make(map[PipelineState]StageFunc),
		logger: logger,
	}
	sm.isCancelled = func(ctx context.Context, reviewID uuid.UUID) (bool, error) {
		status, err := st.GetReviewStatus(ctx, reviewID)
		if err != nil {
			return false, err
		}
		return status == "cancelled", nil
	}
	sm.persist = sm.persistState
	sm.load = sm.loadState
	sm.setStatus = func(ctx context.Context, reviewID uuid.UUID, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error) {
		if len(allowedCurrent) == 0 {
			return true, st.UpdateReviewStatus(ctx, reviewID, status, errMsg, tokenUsage)
		}
		return st.UpdateReviewStatusIf(ctx, reviewID, status, errMsg, tokenUsage, allowedCurrent)
	}
	sm.setAttemptStatus = st.UpdateReviewStatusForAttempt
	sm.currentGeneration = st.GetReviewAttemptGeneration
	return sm
}

func (sm *StateMachine) RegisterStage(state PipelineState, fn StageFunc) {
	sm.stages[state] = fn
}

func (sm *StateMachine) setRunStatus(ctx context.Context, run *PipelineRun, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error) {
	if sm.setAttemptStatus != nil {
		return sm.setAttemptStatus(ctx, run.ReviewID, run.AttemptGeneration, status, errMsg, tokenUsage, allowedCurrent)
	}
	return sm.setStatus(ctx, run.ReviewID, status, errMsg, tokenUsage, allowedCurrent)
}

// Run executes the pipeline from the current state to completion or failure.
func (sm *StateMachine) Run(ctx context.Context, run *PipelineRun) error {
	// Transition review status pending → in_progress on first tick.
	// Historically nothing did this, so every review looked stuck on "pending"
	// until it completed/failed, which broke the dashboard's `isLive` check
	// and stream handshake timing. Non-fatal: log Warn if the DB is down.
	if applied, updErr := sm.setRunStatus(ctx, run, "in_progress", "", nil, []string{"pending", "in_progress", "failed"}); updErr != nil {
		sm.logger.Warn("failed to mark review in_progress", "error", updErr, "review_id", run.ReviewID)
	} else if !applied {
		return context.Canceled
	}

	trans := transitions()
	for !run.State.IsTerminal() {
		select {
		case <-ctx.Done():
			return sm.handleCancelled(ctx, run)
		default:
		}

		// Cooperative cancellation: a Stop handled on another machine (or after
		// this process restarted, or for a retried run whose context this
		// process doesn't hold) can only set the DB cancel flag — it can't
		// cancel our ctx. Check it at each stage boundary so those cancels halt
		// the pipeline before it posts to GitHub.
		if cancelled, err := sm.isCancelled(ctx, run.ReviewID); err != nil {
			sm.logger.Warn("cooperative cancel check failed", "error", err, "review_id", run.ReviewID)
		} else if cancelled {
			sm.logger.Info("cooperative cancel: review flagged cancelled, halting", "review_id", run.ReviewID, "stage", run.State)
			return sm.handleCancelled(ctx, run)
		}

		stage, ok := sm.stages[run.State]
		if !ok {
			// No handler for this state -- advance to next
			next, exists := trans[run.State]
			if !exists {
				return fmt.Errorf("no transition from state %s", run.State)
			}
			run.State = next
			run.UpdatedAt = time.Now()
			publishStageChanged(run)
			if shouldPersist(run.State) {
				if err := sm.persist(ctx, run); err != nil {
					return fmt.Errorf("persisting state: %w", err)
				}
			}
			continue
		}

		sm.logger.Info("executing stage", "state", run.State, "review_id", run.ReviewID)
		stageStart := time.Now()

		if err := stage(ctx, run); err != nil {
			// Context cancelled mid-stage → treat as cancellation, not failure
			if errors.Is(err, context.Canceled) {
				return sm.handleCancelled(ctx, run)
			}

			failedState := run.State
			run.State = StateFailed
			run.Error = err.Error()
			run.UpdatedAt = time.Now()
			if persistErr := sm.persist(context.WithoutCancel(ctx), run); persistErr != nil {
				sm.logger.Error("failed to persist failure state", "error", persistErr, "review_id", run.ReviewID)
			}
			var tokenUsage []byte
			if run.Tokens.Total.TotalTokens > 0 {
				tokenUsage, _ = json.Marshal(&run.Tokens)
			}
			// Conditional: don't overwrite a review another writer already moved
			// to a terminal state — e.g. a cancel that raced this failure.
			applied, persistErr := sm.setRunStatus(context.WithoutCancel(ctx), run, string(StateFailed), run.Error, tokenUsage, []string{"pending", "in_progress"})
			if persistErr != nil {
				sm.logger.Error("failed to update review status on failure", "error", persistErr, "review_id", run.ReviewID)
			}
			// Only when THIS writer won. setStatus is conditional precisely
			// because a cancel can race a failure; the loser must not then
			// rewrite the PR comment to its own outcome and contradict the
			// status that was actually persisted. Detached ctx: the run's
			// context is already dead and the rewrite must still reach GitHub.
			if applied {
				publishError(run, failedState, err)
			}
			if applied && sm.onTerminal != nil {
				sm.onTerminal(context.WithoutCancel(ctx), run.ReviewID, StartedOutcomeFailed, run.Error)
			}
			return fmt.Errorf("stage %s failed: %w", failedState, err)
		}

		stageDurationMs := time.Since(stageStart).Milliseconds()
		// stage.completed fires for every successful stage transition. The
		// attrs are a deliberate subset of the tracker-era signature — token
		// totals are aggregated per-stage on run.Tokens and are emitted via
		// LLM call events (one per Complete() call), so re-serializing them
		// here would both double-count in PostHog dashboards and bloat each
		// record past the slog.Handler buffer's record size budget.
		sm.logger.InfoContext(ctx, "stage completed",
			slog.String("event", "stage.completed"),
			slog.String("review_id", run.ReviewID.String()),
			slog.String("stage", string(run.State)),
			slog.Int64("duration_ms", stageDurationMs),
			slog.Int64("installation_id", run.DBInstallationID),
			slog.String("repo", run.PREvent.RepoFullName),
			slog.Int("pr_number", run.PREvent.PRNumber),
			slog.String("trace_id", run.TraceID),
		)

		next, exists := trans[run.State]
		if !exists {
			return fmt.Errorf("no transition from state %s", run.State)
		}
		run.State = next
		run.UpdatedAt = time.Now()
		publishStageChanged(run)
		if shouldPersist(run.State) {
			if err := sm.persist(ctx, run); err != nil {
				return fmt.Errorf("persisting state: %w", err)
			}
		}
	}
	return nil
}

// publishStageChanged emits a stage_changed event if EventBus is attached.
func publishStageChanged(run *PipelineRun) {
	if run.EventBus == nil {
		return
	}
	run.EventBus.PublishForAttempt(run.ReviewID, run.AttemptGeneration, EventStageChanged, map[string]string{
		"stage": string(run.State),
	})
}

// publishError emits an error event if EventBus is attached.
func publishError(run *PipelineRun, failedStage PipelineState, err error) {
	if run.EventBus == nil {
		return
	}
	run.EventBus.PublishForAttempt(run.ReviewID, run.AttemptGeneration, EventError, map[string]string{
		"stage": string(failedStage),
		"error": err.Error(),
	})
}

// handleCancelled transitions a run to the cancelled state, persists it, and publishes the event.
// Uses context.WithoutCancel so DB writes succeed even after parent context is cancelled.
func (sm *StateMachine) handleCancelled(ctx context.Context, run *PipelineRun) error {
	cancelledAtStage := run.State
	run.State = StateCancelled
	run.Error = "cancelled by user"
	run.UpdatedAt = time.Now()

	dbCtx := context.WithoutCancel(ctx)

	if persistErr := sm.persist(dbCtx, run); persistErr != nil {
		sm.logger.Error("failed to persist cancelled state", "error", persistErr, "review_id", run.ReviewID)
	}
	var tokenUsage []byte
	if run.Tokens.Total.TotalTokens > 0 {
		tokenUsage, _ = json.Marshal(&run.Tokens)
	}
	// Conditional: never flip a review that already reached completed/failed.
	applied, persistErr := sm.setRunStatus(dbCtx, run, "cancelled", run.Error, tokenUsage, []string{"pending", "in_progress"})
	if persistErr != nil {
		sm.logger.Error("failed to update review status on cancel", "error", persistErr, "review_id", run.ReviewID)
	}
	// Same rule as the failure path, and the reason this is not deferred: a
	// deferred callback would fire even when the write was rejected, letting a
	// losing canceller overwrite a comment that already reports the real
	// outcome.
	if applied && run.EventBus != nil {
		run.EventBus.PublishForAttempt(run.ReviewID, run.AttemptGeneration, EventCancelled, map[string]string{"stage": string(cancelledAtStage)})
	}
	if applied && sm.onTerminal != nil {
		sm.onTerminal(dbCtx, run.ReviewID, StartedOutcomeCancelled, "")
	}
	sm.logger.Info("review cancelled", "review_id", run.ReviewID, "stage", cancelledAtStage)
	return context.Canceled
}

// shouldPersist returns true for states worth persisting to DB.
// Triage is fast -- just re-run on recovery. Everything after review is persisted.
func shouldPersist(state PipelineState) bool {
	switch state {
	case StateReviewing, StateBriefing, StateDeduping, StateValidating, StateScoring, StatePass2, StateSynthesizing, StatePosting, StateCompleted, StateFailed, StateCancelled:
		return true
	}
	return false
}

// Resume loads and resumes an incomplete pipeline run.
//
// IMPORTANT: this path must register the review's event-bus topic before
// calling Run, otherwise Publish/Subscribe no-op and WebSocket clients
// reconnect-loop. HandlePREvent opens the topic for new reviews; Resume
// must mirror that for recovered reviews (e.g. after fly deploy restarts).
//
// It must also re-resolve the run context json:"-" dropped on the way into
// pipeline_states — otherwise the resumed run continues with feature flags,
// similarity gates and the memory indexer all at their zero values, which is
// not "the same review, continued" but a differently-configured one. That is
// what hydrate does; see resume_context.go for what it deliberately leaves out.
func (sm *StateMachine) Resume(ctx context.Context, runID uuid.UUID) (*PipelineRun, error) {
	return sm.resume(ctx, runID, 0)
}

func (sm *StateMachine) ResumeAttempt(ctx context.Context, runID uuid.UUID, attemptGeneration int) (*PipelineRun, error) {
	return sm.resume(ctx, runID, attemptGeneration)
}

func (sm *StateMachine) resume(ctx context.Context, runID uuid.UUID, attemptGeneration int) (*PipelineRun, error) {
	run, err := sm.load(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	if attemptGeneration > 0 {
		run.AttemptGeneration = attemptGeneration
	} else if run.AttemptGeneration == 0 && sm.currentGeneration != nil {
		generation, err := sm.currentGeneration(ctx, run.ReviewID)
		if err != nil {
			return nil, fmt.Errorf("loading attempt generation: %w", err)
		}
		run.AttemptGeneration = generation
	}
	run.EventBus = sm.eventBus
	if run.State.IsTerminal() {
		return run, nil
	}
	if sm.eventBus != nil {
		sm.eventBus.OpenTopic(run.ReviewID)
		defer sm.eventBus.CloseTopic(run.ReviewID)
	}
	// Before Run, never inside it: the first stage this loop executes is
	// already a consumer of the flags/thresholds/indexer it restores.
	if sm.hydrate != nil {
		sm.hydrate(ctx, run)
	}
	sm.logger.Info("resuming pipeline", "run_id", runID, "state", run.State)
	return run, sm.Run(ctx, run)
}

func (sm *StateMachine) persistState(ctx context.Context, run *PipelineRun) error {
	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshaling run: %w", err)
	}

	_, err = sm.db.Exec(ctx, `
		INSERT INTO pipeline_states (id, review_id, state, payload, error, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (id) DO UPDATE SET
			state = EXCLUDED.state,
			payload = EXCLUDED.payload,
			error = EXCLUDED.error,
			updated_at = NOW()
	`, run.ID, run.ReviewID, run.State, payload, run.Error)
	if err != nil {
		return fmt.Errorf("upserting pipeline_states: %w", err)
	}
	return nil
}

func (sm *StateMachine) loadState(ctx context.Context, runID uuid.UUID) (*PipelineRun, error) {
	var payload []byte
	err := sm.db.QueryRow(ctx,
		`SELECT payload FROM pipeline_states WHERE id = $1`, runID,
	).Scan(&payload)
	if err != nil {
		return nil, fmt.Errorf("querying state: %w", err)
	}

	var run PipelineRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return nil, fmt.Errorf("unmarshaling run: %w", err)
	}
	return &run, nil
}

// recoverStaleAfter is the minimum updated_at age before RecoverIncomplete
// will claim a non-terminal run. Shorter = faster crash recovery but higher
// risk of picking up a run another machine is actively processing. Longer =
// safer against duplicate execution (observed in production: a
// Fly standby auto-started to serve a dashboard burst, called
// RecoverIncomplete, claimed a live review, and posted a second GitHub
// review 32 s after the first). Observed longest legitimate stage
// turnaround is ~2 min; 10 min gives a ~5× margin while still recovering
// truly-crashed runs within the fly health-check deploy-rollback window.
const recoverStaleAfter = 10 * time.Minute

// RecoverIncomplete resumes non-terminal pipeline runs whose updated_at is
// older than recoverStaleAfter. Rows updated more recently are assumed to
// be owned by another live process; taking them over would double-execute
// the pipeline and post a duplicate GitHub review.
func (sm *StateMachine) RecoverIncomplete(ctx context.Context) error {
	var firstErr error
	for ctx.Err() == nil {
		owner := uuid.New()
		runID, claimed, err := sm.claimIncomplete(ctx, owner)
		if err != nil {
			return fmt.Errorf("claiming incomplete run: %w", err)
		}
		if !claimed {
			return firstErr
		}

		sm.logger.Info("recovering pipeline run", "run_id", runID, "recovery_owner", owner)
		stopHeartbeat := make(chan struct{})
		go sm.renewRecoveryLease(ctx, runID, owner, stopHeartbeat)
		_, resumeErr := sm.Resume(ctx, runID)
		close(stopHeartbeat)
		if resumeErr != nil {
			sm.logger.Error("failed to recover run", "run_id", runID, "error", resumeErr)
			if firstErr == nil {
				firstErr = resumeErr
			}
			// Keep the lease after failure so this invocation can progress to
			// other rows without immediately reclaiming the same broken run.
			continue
		}
		if _, err := sm.db.Exec(ctx, `UPDATE pipeline_states SET recovery_owner=NULL, recovery_lease_until=NULL WHERE id=$1 AND recovery_owner=$2`, runID, owner); err != nil {
			sm.logger.Warn("clearing recovery lease", "run_id", runID, "error", err)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}

const recoveryLeaseDuration = 2 * time.Minute

func (sm *StateMachine) claimIncomplete(ctx context.Context, owner uuid.UUID) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := sm.db.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id FROM pipeline_states
			WHERE state NOT IN ($1,$2,$3)
			  AND updated_at < NOW() - make_interval(secs => $4)
			  AND (recovery_lease_until IS NULL OR recovery_lease_until < NOW())
			ORDER BY updated_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE pipeline_states ps
		SET recovery_owner=$5, recovery_lease_until=NOW() + make_interval(secs => $6)
		FROM candidate WHERE ps.id=candidate.id
		RETURNING ps.id`, StateCompleted, StateFailed, StateCancelled, recoverStaleAfter.Seconds(), owner, recoveryLeaseDuration.Seconds()).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, err
	}
	return id, true, nil
}

func (sm *StateMachine) renewRecoveryLease(ctx context.Context, runID, owner uuid.UUID, stop <-chan struct{}) {
	ticker := time.NewTicker(recoveryLeaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if _, err := sm.db.Exec(ctx, `UPDATE pipeline_states SET recovery_lease_until=NOW()+make_interval(secs => $3) WHERE id=$1 AND recovery_owner=$2`, runID, owner, recoveryLeaseDuration.Seconds()); err != nil {
				sm.logger.Warn("renewing recovery lease", "run_id", runID, "error", err)
			}
		}
	}
}
