// Package pipeline — statemachine_cancel_test.go guards cooperative
// cancellation (a DB-flag cancel halts the stage loop before posting) and the
// retry-while-live refusal. DB-less: the two Postgres mutations Run performs
// (persist, setStatus) and the cancel check are injected via StateMachine seams.
package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
)

// pipelineStages is the ordered set of stages a run walks through. Registered as
// no-op recorders in these tests so Run drives real transitions without work.
var pipelineStages = []PipelineState{
	StateTriaging, StateBriefing, StateReviewing, StateDeduping,
	StateValidating, StateScoring, StatePass2, StateSynthesizing, StatePosting,
}

type statusWrite struct {
	status  string
	allowed []string
}

// newTestSM builds a DB-less StateMachine and returns a pointer to the slice
// that records every setStatus call (status + its allowedCurrent guard).
func newTestSM() (*StateMachine, *[]statusWrite) {
	writes := &[]statusWrite{}
	sm := &StateMachine{
		stages: make(map[PipelineState]StageFunc),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	sm.persist = func(_ context.Context, _ *PipelineRun) error { return nil }
	sm.setStatus = func(_ context.Context, _ uuid.UUID, status, _ string, _ []byte, allowed []string) (bool, error) {
		*writes = append(*writes, statusWrite{status: status, allowed: allowed})
		return true, nil
	}
	sm.isCancelled = func(_ context.Context, _ uuid.UUID) (bool, error) { return false, nil }
	return sm, writes
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRun_CooperativeCancel_HaltsBeforePost proves that flagging the review
// cancelled mid-run (via the isCancelled seam) halts the loop before the
// posting stage executes, and that the cancelled status write is a
// compare-and-set (so it can't clobber a completed review).
func TestRun_CooperativeCancel_HaltsBeforePost(t *testing.T) {
	sm, writes := newTestSM()
	run := &PipelineRun{ReviewID: uuid.New(), State: StatePending}

	var executed []PipelineState
	posted := false
	for _, st := range pipelineStages {
		st := st
		sm.RegisterStage(st, func(_ context.Context, _ *PipelineRun) error {
			executed = append(executed, st)
			if st == StatePosting {
				posted = true
			}
			return nil
		})
	}
	// Flag cancelled exactly when the loop is about to run the posting stage.
	sm.isCancelled = func(_ context.Context, _ uuid.UUID) (bool, error) {
		return run.State == StatePosting, nil
	}

	err := sm.Run(context.Background(), run)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	if run.State != StateCancelled {
		t.Errorf("run.State = %q, want %q", run.State, StateCancelled)
	}
	if posted {
		t.Error("posting stage executed despite cancel — must halt before posting")
	}
	if len(executed) == 0 {
		t.Error("expected earlier stages to execute before the cancel")
	}
	for _, s := range executed {
		if s == StatePosting {
			t.Error("posting stage ran")
		}
	}
	var sawCancelled bool
	for _, w := range *writes {
		if w.status == "cancelled" {
			sawCancelled = true
			if !equalStrings(w.allowed, []string{"pending", "in_progress"}) {
				t.Errorf("cancelled write allowedCurrent = %v, want [pending in_progress] (must be conditional)", w.allowed)
			}
		}
	}
	if !sawCancelled {
		t.Error("no cancelled status write recorded")
	}
}

// TestRun_HappyPath_RunsToCompletion proves the cooperative check doesn't break
// the normal path: with no cancel flag, every stage runs and the run completes.
func TestRun_HappyPath_RunsToCompletion(t *testing.T) {
	sm, writes := newTestSM()
	run := &PipelineRun{ReviewID: uuid.New(), State: StatePending}

	posted := false
	for _, st := range pipelineStages {
		st := st
		sm.RegisterStage(st, func(_ context.Context, _ *PipelineRun) error {
			if st == StatePosting {
				posted = true
			}
			return nil
		})
	}

	if err := sm.Run(context.Background(), run); err != nil {
		t.Fatalf("Run err = %v, want nil", err)
	}
	if run.State != StateCompleted {
		t.Errorf("run.State = %q, want %q", run.State, StateCompleted)
	}
	if !posted {
		t.Error("posting stage should have executed on the happy path")
	}
	for _, w := range *writes {
		if w.status == "cancelled" || w.status == "failed" {
			t.Errorf("unexpected %q status write on happy path", w.status)
		}
	}
}

// TestRun_StageFailure_WritesFailedConditionally proves a stage failure writes
// "failed" as a compare-and-set (so a racing cancel isn't clobbered).
func TestRun_StageFailure_WritesFailedConditionally(t *testing.T) {
	sm, writes := newTestSM()
	run := &PipelineRun{ReviewID: uuid.New(), State: StatePending}
	boom := errors.New("boom")
	sm.RegisterStage(StateTriaging, func(_ context.Context, _ *PipelineRun) error { return boom })

	err := sm.Run(context.Background(), run)
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want boom", err)
	}
	if run.State != StateFailed {
		t.Errorf("run.State = %q, want %q", run.State, StateFailed)
	}
	var sawFailed bool
	for _, w := range *writes {
		if w.status == "failed" {
			sawFailed = true
			if !equalStrings(w.allowed, []string{"pending", "in_progress"}) {
				t.Errorf("failed write allowedCurrent = %v, want [pending in_progress] (must be conditional)", w.allowed)
			}
		}
	}
	if !sawFailed {
		t.Error("no failed status write recorded")
	}
}

// TestShouldRefuseRetry pins the retry-refusal rule: only a non-terminal run
// that is still fresh (likely live) blocks a retry.
func TestShouldRefuseRetry(t *testing.T) {
	cases := []struct {
		name  string
		state PipelineState
		fresh bool
		want  bool
	}{
		{"fresh live run refused", StateReviewing, true, true},
		{"stale non-terminal run allowed (crashed)", StateReviewing, false, false},
		{"terminal failed allowed", StateFailed, true, false},
		{"terminal cancelled allowed", StateCancelled, true, false},
		{"terminal completed allowed", StateCompleted, true, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRefuseRetry(tc.state, tc.fresh); got != tc.want {
				t.Errorf("shouldRefuseRetry(%q, %v) = %v, want %v", tc.state, tc.fresh, got, tc.want)
			}
		})
	}
}
