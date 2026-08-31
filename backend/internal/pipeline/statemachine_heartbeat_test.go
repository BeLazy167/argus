// Package pipeline — statemachine_heartbeat_test.go guards the stage freshness
// heartbeat: while a stage executes, the run's pipeline_states row is touched
// so the periodic recovery sweeper never claims a live run as crashed. DB-less:
// the touch and the heartbeat tick are injected via StateMachine seams.
package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// sendTick delivers one heartbeat tick, failing fast instead of hanging the
// whole test binary if no heartbeat goroutine is listening.
func sendTick(t *testing.T, tick chan<- time.Time) {
	t.Helper()
	select {
	case tick <- time.Now():
	case <-time.After(5 * time.Second):
		t.Fatal("no heartbeat goroutine consumed the tick")
	}
}

// TestRun_StageHeartbeat_TouchesRunDuringStage proves a tick that fires while a
// stage is executing touches that run's row — the whole point, since a stage
// can outlive the sweeper's claim floor.
func TestRun_StageHeartbeat_TouchesRunDuringStage(t *testing.T) {
	sm, _ := newTestSM()
	run := &PipelineRun{ID: uuid.New(), ReviewID: uuid.New(), State: StatePending}

	tick := make(chan time.Time)
	sm.stageHeartbeat = tick
	touched := make(chan uuid.UUID, 4*len(pipelineStages))
	sm.touchRun = func(_ context.Context, runID uuid.UUID) error {
		touched <- runID
		return nil
	}

	for _, st := range pipelineStages {
		sm.RegisterStage(st, func(_ context.Context, _ *PipelineRun) error { return nil })
	}
	sm.RegisterStage(StateReviewing, func(_ context.Context, _ *PipelineRun) error {
		sendTick(t, tick)
		select {
		case id := <-touched:
			if id != run.ID {
				t.Errorf("touched run %s, want %s", id, run.ID)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("heartbeat tick during stage execution never touched the run")
		}
		return nil
	})

	if err := sm.Run(context.Background(), run); err != nil {
		t.Fatalf("Run err = %v, want nil", err)
	}
}

// TestRun_StageHeartbeat_TouchErrorDoesNotFailRun pins the log-only contract.
// Freshness protects a run from being mistaken for a crash; it is never a
// reason to abort a review, and the claim floor leaves room for missed touches.
func TestRun_StageHeartbeat_TouchErrorDoesNotFailRun(t *testing.T) {
	sm, writes := newTestSM()
	run := &PipelineRun{ID: uuid.New(), ReviewID: uuid.New(), State: StatePending}

	tick := make(chan time.Time)
	sm.stageHeartbeat = tick
	attempted := make(chan struct{}, 4*len(pipelineStages))
	sm.touchRun = func(_ context.Context, _ uuid.UUID) error {
		attempted <- struct{}{}
		return context.DeadlineExceeded
	}

	for _, st := range pipelineStages {
		sm.RegisterStage(st, func(_ context.Context, _ *PipelineRun) error { return nil })
	}
	sm.RegisterStage(StateScoring, func(_ context.Context, _ *PipelineRun) error {
		sendTick(t, tick)
		select {
		case <-attempted:
		case <-time.After(5 * time.Second):
			t.Fatal("heartbeat tick never attempted a touch")
		}
		return nil
	})

	if err := sm.Run(context.Background(), run); err != nil {
		t.Fatalf("Run err = %v, want nil (touch failures must be log-only)", err)
	}
	if run.State != StateCompleted {
		t.Errorf("run.State = %q, want %q", run.State, StateCompleted)
	}
	for _, w := range *writes {
		if w.status == "failed" || w.status == "cancelled" {
			t.Errorf("touch failure produced a %q status write; it must be log-only", w.status)
		}
	}
}

// TestRecoveryClaimFloorExceedsHeartbeat pins the margin the log-only design
// depends on: a live run must miss many consecutive touches before another
// machine may claim it, or periodic scanning would steal live work and post a
// duplicate review.
func TestRecoveryClaimFloorExceedsHeartbeat(t *testing.T) {
	if recoveryClaimStaleAfter <= stageHeartbeatInterval {
		t.Fatalf("claim floor %v must exceed the heartbeat interval %v", recoveryClaimStaleAfter, stageHeartbeatInterval)
	}
	if missed := recoveryClaimStaleAfter / stageHeartbeatInterval; missed < 5 {
		t.Errorf("a live run may be claimed after only %d missed heartbeats; want a wider margin", missed)
	}
	if recoveryClaimStaleAfter < recoverStaleAfter {
		t.Error("recovery must not claim runs the retry-liveness check still considers fresh")
	}
}

// TestRun_StageHeartbeat_PanicStopsHeartbeat proves a panicking stage does not
// leak the heartbeat goroutine. A leaked heartbeat keeps refreshing
// updated_at, so the row never crosses the claim floor and the run is stranded
// forever — recreating the very incident this change fixes.
func TestRun_StageHeartbeat_PanicStopsHeartbeat(t *testing.T) {
	sm, _ := newTestSM()
	run := &PipelineRun{ID: uuid.New(), ReviewID: uuid.New(), State: StatePending}

	tick := make(chan time.Time)
	sm.stageHeartbeat = tick
	touched := make(chan uuid.UUID, 8)
	sm.touchRun = func(_ context.Context, id uuid.UUID) error {
		touched <- id
		return nil
	}
	for _, st := range pipelineStages {
		sm.RegisterStage(st, func(_ context.Context, _ *PipelineRun) error { return nil })
	}
	sm.RegisterStage(pipelineStages[0], func(_ context.Context, _ *PipelineRun) error {
		panic("stage exploded")
	})

	func() {
		defer func() { _ = recover() }()
		_ = sm.Run(context.Background(), run)
	}()

	// The heartbeat must be gone: nothing should consume a tick any more.
	select {
	case tick <- time.Now():
		t.Fatal("heartbeat goroutine leaked past a stage panic — it would keep the row fresh and hide the run from recovery forever")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestRecoveryBackoffSkipsPoisonedRuns pins the bound on repeated recovery of a
// permanently broken run. Without it the 5-minute sweep re-attempts an
// uninstalled repo's run for the process lifetime, where boot-only recovery
// attempted it once.
func TestRecoveryBackoffSkipsPoisonedRuns(t *testing.T) {
	sm, _ := newTestSM()
	now := time.Now()
	active, expired := uuid.New(), uuid.New()
	sm.recordRecoveryBackoff(active, now.Add(time.Hour))
	sm.recordRecoveryBackoff(expired, now.Add(-time.Second))

	skip := sm.recoveryBackoffSkipList(now)
	if len(skip) != 1 || skip[0] != active.String() {
		t.Fatalf("skip list = %v, want only the run still inside its backoff (%s)", skip, active)
	}
	if _, lingers := sm.recoveryBackoff[expired]; lingers {
		t.Error("expired backoff entry was not pruned — the map would grow without bound")
	}
}

// TestRecoverClaimedDamped_RecordsOnFailureAndPanic proves both damping paths.
// The panic path especially: it unwinds past any plain statement, so a
// panicking run would otherwise be re-claimed FIRST on every sweep (claims
// order by updated_at) and starve every other stranded run.
func TestRecoverClaimedDamped_RecordsOnFailureAndPanic(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		sm, _ := newTestSM()
		runID := uuid.New()
		sm.resumeRecoveryFn = func(context.Context, uuid.UUID) (*PipelineRun, error) {
			return nil, errors.New("uninstalled repo")
		}
		_ = sm.recoverClaimedDamped(context.Background(), runID, uuid.New())
		if skip := sm.recoveryBackoffSkipList(time.Now()); len(skip) != 1 || skip[0] != runID.String() {
			t.Fatalf("skip list = %v, want the failed run damped", skip)
		}
	})

	t.Run("panic", func(t *testing.T) {
		sm, _ := newTestSM()
		runID := uuid.New()
		sm.resumeRecoveryFn = func(context.Context, uuid.UUID) (*PipelineRun, error) {
			panic("poisoned payload")
		}
		func() {
			defer func() { _ = recover() }()
			_ = sm.recoverClaimedDamped(context.Background(), runID, uuid.New())
		}()
		if skip := sm.recoveryBackoffSkipList(time.Now()); len(skip) != 1 || skip[0] != runID.String() {
			t.Fatalf("skip list = %v, want the panicking run damped", skip)
		}
	})

}

// TestRecoverClaimed_PanicStopsLeaseRenewal pins the lease-renewal cleanup. A
// panic in the resume path must not leak the renewer: leaseCtx descends from
// the process-lifetime context, so a leaked goroutine pushes
// recovery_lease_until forward forever and claimIncomplete then excludes the
// run on EVERY machine — a permanent strand, the failure this change removes.
func TestRecoverClaimed_PanicStopsLeaseRenewal(t *testing.T) {
	sm, _ := newTestSM()
	tick := make(chan time.Time)
	sm.recoveryHeartbeat = tick
	renews := 0
	sm.renewRecoveryLeaseFn = func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
		renews++
		return true, nil
	}
	sm.resumeRecoveryFn = func(context.Context, uuid.UUID) (*PipelineRun, error) {
		panic("poisoned payload")
	}

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		_ = sm.recoverClaimed(context.Background(), uuid.New(), uuid.New())
	}()
	if !panicked {
		t.Fatal("panic must still propagate to the sweeper's per-scan recover")
	}

	select {
	case tick <- time.Now():
		t.Fatal("lease heartbeat leaked past the panic — it would renew the lease forever and strand the run on every machine")
	case <-time.After(300 * time.Millisecond):
	}
	if renews != 0 {
		t.Errorf("lease renewed %d time(s) after the panic", renews)
	}
}

// TestShouldReenrichOnResume pins the one exception to "resume does not re-run
// enrichers". The recovery sweeper is the ingress the triaging row exists to
// serve, so without this it auto-posts full-price reviews with no intent, SAST,
// arch context or linked issues.
func TestShouldReenrichOnResume(t *testing.T) {
	cases := []struct {
		state  PipelineState
		intent *PRIntent
		want   bool
		why    string
	}{
		{StateTriaging, nil, true, "triage crash: enrichers never consumed, the posted review would be missing whole sections"},
		{StateTriaging, &PRIntent{}, false, "already enriched: must not re-spend the intent call"},
		{StateReviewing, nil, false, "past triage: enrichers already consumed, prompts merely thinner"},
		{StatePosting, nil, false, "past triage"},
	}
	for _, tc := range cases {
		run := &PipelineRun{ID: uuid.New(), ReviewID: uuid.New(), State: tc.state, PRIntent: tc.intent}
		if got := shouldReenrichOnResume(run); got != tc.want {
			t.Errorf("state=%q intent=%v: re-enrich = %v, want %v (%s)", tc.state, tc.intent != nil, got, tc.want, tc.why)
		}
	}
	if shouldReenrichOnResume(nil) {
		t.Error("nil run must not re-enrich")
	}
}

// TestRecoverClaimedDamped_ShutdownExemptionUsesContext pins that the "not a
// poisoned run" exemption keys on a real shutdown. A cooperative cancel also
// returns context.Canceled, and if its persist fails the run stays claimable —
// exempting it by error shape would re-attempt it every sweep forever.
func TestRecoverClaimedDamped_ShutdownExemptionUsesContext(t *testing.T) {
	t.Run("live context, cancelled-shaped error still damps", func(t *testing.T) {
		sm, _ := newTestSM()
		runID := uuid.New()
		sm.resumeRecoveryFn = func(context.Context, uuid.UUID) (*PipelineRun, error) {
			return nil, context.Canceled
		}
		_ = sm.recoverClaimedDamped(context.Background(), runID, uuid.New())
		if skip := sm.recoveryBackoffSkipList(time.Now()); len(skip) != 1 {
			t.Fatalf("skip list = %v, want the run damped: the process is alive, so this was a cooperative cancel", skip)
		}
	})

	t.Run("real shutdown does not damp", func(t *testing.T) {
		sm, _ := newTestSM()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sm.resumeRecoveryFn = func(context.Context, uuid.UUID) (*PipelineRun, error) {
			return nil, context.Canceled
		}
		_ = sm.recoverClaimedDamped(ctx, uuid.New(), uuid.New())
		if skip := sm.recoveryBackoffSkipList(time.Now()); len(skip) != 0 {
			t.Fatalf("skip list = %v, want empty: a shutdown is not a poisoned run", skip)
		}
	})
}
