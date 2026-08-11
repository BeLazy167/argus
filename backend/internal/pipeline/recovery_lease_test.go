package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRecoveredRunCancelsImmediatelyWhenLeaseIsStolen(t *testing.T) {
	ticks := make(chan time.Time, 1)
	resumeStarted := make(chan struct{})
	var mutationAfterCancellation atomic.Bool
	var released atomic.Bool
	sm := &StateMachine{
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		recoveryHeartbeat: ticks,
		renewRecoveryLeaseFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			return false, nil
		},
		resumeRecoveryFn: func(ctx context.Context, _ uuid.UUID) (*PipelineRun, error) {
			close(resumeStarted)
			<-ctx.Done()
			// A recovered stage must observe cancellation before its next
			// externally visible mutation (including review posting).
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			default:
				mutationAfterCancellation.Store(true)
				return nil, nil
			}
		},
		releaseRecoveryLeaseFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			released.Store(true)
			return true, nil
		},
	}

	done := make(chan error, 1)
	go func() { done <- sm.recoverClaimed(context.Background(), uuid.New(), uuid.New()) }()
	<-resumeStarted
	ticks <- time.Now()

	select {
	case err := <-done:
		if !errors.Is(err, errRecoveryLeaseLost) {
			t.Fatalf("recover error=%v, want lease lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("recovered run was not cancelled after lease theft")
	}
	if mutationAfterCancellation.Load() {
		t.Fatal("pipeline mutated after lease cancellation")
	}
	if released.Load() {
		t.Fatal("stolen lease was released by its former owner")
	}
}

func TestRecoveredRunCancelsOnLeaseRenewalFailure(t *testing.T) {
	ticks := make(chan time.Time, 1)
	resumeStarted := make(chan struct{})
	renewalFailure := errors.New("database unavailable")
	sm := &StateMachine{
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		recoveryHeartbeat: ticks,
		renewRecoveryLeaseFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			return false, renewalFailure
		},
		resumeRecoveryFn: func(ctx context.Context, _ uuid.UUID) (*PipelineRun, error) {
			close(resumeStarted)
			<-ctx.Done()
			return nil, context.Cause(ctx)
		},
	}

	done := make(chan error, 1)
	go func() { done <- sm.recoverClaimed(context.Background(), uuid.New(), uuid.New()) }()
	<-resumeStarted
	ticks <- time.Now()
	select {
	case err := <-done:
		if !errors.Is(err, errRecoveryLeaseLost) || !errors.Is(err, renewalFailure) {
			t.Fatalf("recover error=%v, want lease-lost renewal failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("recovered run was not cancelled after renewal failure")
	}
}

func TestRecoveredRunWaitsForHeartbeatAndReleasesWithOwnerCAS(t *testing.T) {
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	renewStarted := make(chan struct{})
	finishRenewal := make(chan struct{})
	owner := uuid.New()
	var releasedOwner uuid.UUID
	released := make(chan struct{})
	sm := &StateMachine{
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		recoveryHeartbeat: ticks,
		renewRecoveryLeaseFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			close(renewStarted)
			<-finishRenewal
			return true, nil
		},
		resumeRecoveryFn: func(context.Context, uuid.UUID) (*PipelineRun, error) {
			<-renewStarted
			return &PipelineRun{}, nil
		},
		releaseRecoveryLeaseFn: func(_ context.Context, _ uuid.UUID, gotOwner uuid.UUID) (bool, error) {
			releasedOwner = gotOwner
			close(released)
			return true, nil
		},
	}

	done := make(chan error, 1)
	go func() { done <- sm.recoverClaimed(context.Background(), uuid.New(), owner) }()
	<-renewStarted
	select {
	case <-released:
		t.Fatal("lease released before in-flight heartbeat stopped")
	default:
	}
	close(finishRenewal)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if releasedOwner != owner {
		t.Fatalf("released owner=%s want %s", releasedOwner, owner)
	}
}

func TestRunDoesNotPersistCancellationAfterRecoveryLeaseLoss(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errRecoveryLeaseLost)
	var mutations atomic.Int32
	sm := &StateMachine{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		setStatus: func(context.Context, uuid.UUID, string, string, []byte, []string) (bool, error) {
			mutations.Add(1)
			return true, nil
		},
		persist: func(context.Context, *PipelineRun) error {
			mutations.Add(1)
			return nil
		},
		isCancelled: func(context.Context, uuid.UUID) (bool, error) { return false, nil },
	}
	run := &PipelineRun{ID: uuid.New(), ReviewID: uuid.New(), State: StateReviewing}
	if err := sm.Run(ctx, run); !errors.Is(err, errRecoveryLeaseLost) {
		t.Fatalf("Run error=%v, want lease lost", err)
	}
	if got := mutations.Load(); got != 0 {
		t.Fatalf("mutations after lost lease=%d want 0", got)
	}
}

func TestRecoveredRunTreatsOwnerCASReleaseMissAsLeaseLoss(t *testing.T) {
	sm := &StateMachine{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		resumeRecoveryFn: func(context.Context, uuid.UUID) (*PipelineRun, error) {
			return &PipelineRun{}, nil
		},
		releaseRecoveryLeaseFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			return false, nil
		},
	}
	if err := sm.recoverClaimed(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, errRecoveryLeaseLost) {
		t.Fatalf("recover error=%v, want lease lost", err)
	}
}
