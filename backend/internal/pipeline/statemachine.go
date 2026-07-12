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
	// persist and setStatus wrap the two Postgres mutations the stage loop
	// performs, so Run can be exercised without a live DB. Defaults wired in
	// NewStateMachine. setStatus is a compare-and-set: an empty allowedCurrent
	// means an unconditional write.
	persist   func(ctx context.Context, run *PipelineRun) error
	setStatus func(ctx context.Context, reviewID uuid.UUID, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error)
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
	sm.setStatus = func(ctx context.Context, reviewID uuid.UUID, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error) {
		if len(allowedCurrent) == 0 {
			return true, st.UpdateReviewStatus(ctx, reviewID, status, errMsg, tokenUsage)
		}
		return st.UpdateReviewStatusIf(ctx, reviewID, status, errMsg, tokenUsage, allowedCurrent)
	}
	return sm
}

func (sm *StateMachine) RegisterStage(state PipelineState, fn StageFunc) {
	sm.stages[state] = fn
}

// Run executes the pipeline from the current state to completion or failure.
func (sm *StateMachine) Run(ctx context.Context, run *PipelineRun) error {
	// Transition review status pending → in_progress on first tick.
	// Historically nothing did this, so every review looked stuck on "pending"
	// until it completed/failed, which broke the dashboard's `isLive` check
	// and stream handshake timing. Non-fatal: log Warn if the DB is down.
	if _, updErr := sm.setStatus(ctx, run.ReviewID, "in_progress", "", nil, nil); updErr != nil {
		sm.logger.Warn("failed to mark review in_progress", "error", updErr, "review_id", run.ReviewID)
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
			publishError(run, failedState, err)
			if persistErr := sm.persist(context.WithoutCancel(ctx), run); persistErr != nil {
				sm.logger.Error("failed to persist failure state", "error", persistErr, "review_id", run.ReviewID)
			}
			var tokenUsage []byte
			if run.Tokens.Total.TotalTokens > 0 {
				tokenUsage, _ = json.Marshal(&run.Tokens)
			}
			// Conditional: don't overwrite a review another writer already moved
			// to a terminal state — e.g. a cancel that raced this failure.
			if _, persistErr := sm.setStatus(context.WithoutCancel(ctx), run.ReviewID, string(StateFailed), run.Error, tokenUsage, []string{"pending", "in_progress"}); persistErr != nil {
				sm.logger.Error("failed to update review status on failure", "error", persistErr, "review_id", run.ReviewID)
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
	run.EventBus.Publish(run.ReviewID, EventStageChanged, map[string]string{
		"stage": string(run.State),
	})
}

// publishError emits an error event if EventBus is attached.
func publishError(run *PipelineRun, failedStage PipelineState, err error) {
	if run.EventBus == nil {
		return
	}
	run.EventBus.Publish(run.ReviewID, EventError, map[string]string{
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

	if run.EventBus != nil {
		run.EventBus.Publish(run.ReviewID, EventCancelled, map[string]string{
			"stage": string(cancelledAtStage),
		})
	}

	if persistErr := sm.persist(dbCtx, run); persistErr != nil {
		sm.logger.Error("failed to persist cancelled state", "error", persistErr, "review_id", run.ReviewID)
	}
	var tokenUsage []byte
	if run.Tokens.Total.TotalTokens > 0 {
		tokenUsage, _ = json.Marshal(&run.Tokens)
	}
	// Conditional: never flip a review that already reached completed/failed.
	if _, persistErr := sm.setStatus(dbCtx, run.ReviewID, "cancelled", run.Error, tokenUsage, []string{"pending", "in_progress"}); persistErr != nil {
		sm.logger.Error("failed to update review status on cancel", "error", persistErr, "review_id", run.ReviewID)
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
func (sm *StateMachine) Resume(ctx context.Context, runID uuid.UUID) (*PipelineRun, error) {
	run, err := sm.loadState(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("loading state: %w", err)
	}
	run.EventBus = sm.eventBus
	if run.State.IsTerminal() {
		return run, nil
	}
	if sm.eventBus != nil {
		sm.eventBus.OpenTopic(run.ReviewID)
		defer sm.eventBus.CloseTopic(run.ReviewID)
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
// safer against duplicate execution (see AcmeOrg PR #510 on 2026-04-23: a
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
	rows, err := sm.db.Query(ctx,
		`SELECT id FROM pipeline_states
		 WHERE state NOT IN ($1, $2, $3)
		   AND updated_at < NOW() - make_interval(secs => $4)
		 ORDER BY updated_at`,
		StateCompleted, StateFailed, StateCancelled,
		recoverStaleAfter.Seconds(),
	)
	if err != nil {
		return fmt.Errorf("querying incomplete runs: %w", err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scanning id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating rows: %w", err)
	}

	var firstErr error
	for _, id := range ids {
		sm.logger.Info("recovering pipeline run", "run_id", id)
		if _, err := sm.Resume(ctx, id); err != nil {
			sm.logger.Error("failed to recover run", "run_id", id, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
