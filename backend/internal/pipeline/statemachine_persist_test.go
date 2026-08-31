// Package pipeline — statemachine_persist_test.go pins which states persist a
// pipeline_states row. The triaging persist is load-bearing for recovery: the
// row's existence is what makes a crashed run visible to the sweeper, so a
// run must be persisted BEFORE its first stage executes, not after.
package pipeline

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestRun_PersistsTriagingBeforeFirstStage proves a row exists by the time the
// triaging stage starts executing. Without it, a crash during triage strands
// the review as in_progress with no pipeline_states row for claimIncomplete
// to find — the exact half of the 2026-08-29 incident class that outlived the
// sweeper's introduction.
func TestRun_PersistsTriagingBeforeFirstStage(t *testing.T) {
	sm, _ := newTestSM()
	run := &PipelineRun{ID: uuid.New(), ReviewID: uuid.New(), State: StatePending}

	var persisted []PipelineState
	sm.persist = func(_ context.Context, got *PipelineRun) error {
		persisted = append(persisted, got.State)
		return nil
	}

	var persistedWhenTriageRan []PipelineState
	for _, st := range pipelineStages {
		sm.RegisterStage(st, func(_ context.Context, _ *PipelineRun) error { return nil })
	}
	sm.RegisterStage(StateTriaging, func(_ context.Context, _ *PipelineRun) error {
		persistedWhenTriageRan = append([]PipelineState(nil), persisted...)
		return nil
	})

	if err := sm.Run(context.Background(), run); err != nil {
		t.Fatalf("Run err = %v, want nil", err)
	}

	found := false
	for _, st := range persistedWhenTriageRan {
		if st == StateTriaging {
			found = true
		}
	}
	if !found {
		t.Fatalf("triaging stage executed before its run was persisted (persisted at that point: %v) — a crash mid-triage would be invisible to the recovery sweeper", persistedWhenTriageRan)
	}
}
