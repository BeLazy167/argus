package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StateMachine drives a PipelineRun through stages, persisting state to Postgres.
type StateMachine struct {
	db       *pgxpool.Pool
	st       *store.Store
	stages   map[PipelineState]StageFunc
	eventBus *EventBus
	tracker  EventTracker
	logger   *slog.Logger
}

func NewStateMachine(db *pgxpool.Pool, st *store.Store, logger *slog.Logger) *StateMachine {
	return &StateMachine{
		db:     db,
		st:     st,
		stages: make(map[PipelineState]StageFunc),
		logger: logger,
	}
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
	if updErr := sm.st.UpdateReviewStatus(ctx, run.ReviewID, "in_progress", "", nil); updErr != nil {
		sm.logger.Warn("failed to mark review in_progress", "error", updErr, "review_id", run.ReviewID)
	}

	trans := transitions()
	for !run.State.IsTerminal() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
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
				if err := sm.persistState(ctx, run); err != nil {
					return fmt.Errorf("persisting state: %w", err)
				}
			}
			continue
		}

		sm.logger.Info("executing stage", "state", run.State, "review_id", run.ReviewID)
		stageStart := time.Now()

		if err := stage(ctx, run); err != nil {
			failedState := run.State
			run.State = StateFailed
			run.Error = err.Error()
			run.UpdatedAt = time.Now()
			publishError(run, failedState, err)
			if persistErr := sm.persistState(ctx, run); persistErr != nil {
				sm.logger.Error("failed to persist failure state", "error", persistErr, "review_id", run.ReviewID)
			}
			var tokenUsage []byte
			if run.Tokens.Total.TotalTokens > 0 {
				tokenUsage, _ = json.Marshal(run.Tokens)
			}
			if persistErr := sm.st.UpdateReviewStatus(ctx, run.ReviewID, string(StateFailed), run.Error, tokenUsage); persistErr != nil {
				sm.logger.Error("failed to update review status on failure", "error", persistErr, "review_id", run.ReviewID)
			}
			return fmt.Errorf("stage %s failed: %w", failedState, err)
		}

		if sm.tracker != nil {
			sm.tracker.TrackStageCompleted(run.DBInstallationID, run.PREvent.RepoFullName, run.PREvent.PRNumber, run.ReviewID.String(), string(run.State), time.Since(stageStart).Milliseconds())
		}

		next, exists := trans[run.State]
		if !exists {
			return fmt.Errorf("no transition from state %s", run.State)
		}
		run.State = next
		run.UpdatedAt = time.Now()
		publishStageChanged(run)
		if shouldPersist(run.State) {
			if err := sm.persistState(ctx, run); err != nil {
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

// shouldPersist returns true for states worth persisting to DB.
// Triage is fast -- just re-run on recovery. Everything after review is persisted.
func shouldPersist(state PipelineState) bool {
	switch state {
	case StateReviewing, StateBriefing, StateDeduping, StateValidating, StateScoring, StateBroadcasting, StateCrossChecking, StatePass2, StateSynthesizing, StatePosting, StateCompleted, StateFailed:
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

// RecoverIncomplete finds all non-terminal pipeline runs and resumes them.
func (sm *StateMachine) RecoverIncomplete(ctx context.Context) error {
	rows, err := sm.db.Query(ctx,
		`SELECT id FROM pipeline_states WHERE state NOT IN ($1, $2) ORDER BY updated_at`,
		StateCompleted, StateFailed,
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
