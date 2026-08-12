package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/google/uuid"
)

type recoveryContextKey struct{}

func TestRecoveryReconcilesPersistedPREventBeforeResume(t *testing.T) {
	owner := uuid.New()
	runID := uuid.New()
	persisted := ghpkg.PREvent{
		InstallationID: 91,
		RepoFullName:   "acme/api",
		PRNumber:       17,
	}
	order := make([]string, 0, 2)
	var releasedOwner uuid.UUID
	baseCtx := context.WithValue(context.Background(), recoveryContextKey{}, "request-value")

	sm := &StateMachine{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		load: func(context.Context, uuid.UUID) (*PipelineRun, error) {
			return &PipelineRun{PREvent: persisted}, nil
		},
		reconcileRecoveryReactions: func(ctx context.Context, installationID int64, repoFullName string, prNumber int) error {
			order = append(order, "reconcile")
			if got := ctx.Value(recoveryContextKey{}); got != "request-value" {
				t.Fatalf("reconciliation context value = %v, want request-value", got)
			}
			got := ghpkg.PREvent{InstallationID: installationID, RepoFullName: repoFullName, PRNumber: prNumber}
			if !reflect.DeepEqual(got, persisted) {
				t.Fatalf("reconciliation identity = %+v, want persisted %+v", got, persisted)
			}
			return nil
		},
		resumeRecoveryFn: func(ctx context.Context, gotRunID uuid.UUID) (*PipelineRun, error) {
			order = append(order, "resume")
			if gotRunID != runID {
				t.Fatalf("resume run id = %s, want %s", gotRunID, runID)
			}
			if got := ctx.Value(recoveryContextKey{}); got != "request-value" {
				t.Fatalf("resume context value = %v, want request-value", got)
			}
			return &PipelineRun{}, nil
		},
		releaseRecoveryLeaseFn: func(_ context.Context, _ uuid.UUID, gotOwner uuid.UUID) (bool, error) {
			releasedOwner = gotOwner
			return true, nil
		},
	}

	if err := sm.recoverClaimed(baseCtx, runID, owner); err != nil {
		t.Fatalf("recoverClaimed: %v", err)
	}
	if !reflect.DeepEqual(order, []string{"reconcile", "resume"}) {
		t.Fatalf("recovery order = %v, want [reconcile resume]", order)
	}
	if releasedOwner != owner {
		t.Fatalf("released owner = %s, want %s", releasedOwner, owner)
	}
}

func TestRecoveryReactionFailurePreventsResumeAndRetainsLease(t *testing.T) {
	sweepErr := errors.New("GitHub rate limit")
	var resumed, released bool
	sm := &StateMachine{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		load: func(context.Context, uuid.UUID) (*PipelineRun, error) {
			return &PipelineRun{PREvent: ghpkg.PREvent{
				InstallationID: 91,
				RepoFullName:   "acme/api",
				PRNumber:       17,
			}}, nil
		},
		reconcileRecoveryReactions: func(context.Context, int64, string, int) error {
			return sweepErr
		},
		resumeRecoveryFn: func(context.Context, uuid.UUID) (*PipelineRun, error) {
			resumed = true
			return &PipelineRun{}, nil
		},
		releaseRecoveryLeaseFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			released = true
			return true, nil
		},
	}

	err := sm.recoverClaimed(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, sweepErr) {
		t.Fatalf("recoverClaimed error = %v, want wrapped sweep failure", err)
	}
	if resumed {
		t.Fatal("Resume ran after reaction reconciliation failed")
	}
	if released {
		t.Fatal("recovery lease was released after reaction reconciliation failed")
	}
}

func TestRecoveryLeaseLossCancelsReactionReconciliationBeforeResume(t *testing.T) {
	ticks := make(chan time.Time, 1)
	reconcileStarted := make(chan struct{})
	var resumed, released bool
	sm := &StateMachine{
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		recoveryHeartbeat: ticks,
		load: func(context.Context, uuid.UUID) (*PipelineRun, error) {
			return &PipelineRun{PREvent: ghpkg.PREvent{
				InstallationID: 91,
				RepoFullName:   "acme/api",
				PRNumber:       17,
			}}, nil
		},
		reconcileRecoveryReactions: func(ctx context.Context, _ int64, _ string, _ int) error {
			close(reconcileStarted)
			<-ctx.Done()
			return context.Cause(ctx)
		},
		renewRecoveryLeaseFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			return false, nil
		},
		resumeRecoveryFn: func(context.Context, uuid.UUID) (*PipelineRun, error) {
			resumed = true
			return &PipelineRun{}, nil
		},
		releaseRecoveryLeaseFn: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			released = true
			return true, nil
		},
	}

	done := make(chan error, 1)
	go func() { done <- sm.recoverClaimed(context.Background(), uuid.New(), uuid.New()) }()
	<-reconcileStarted
	ticks <- time.Now()

	select {
	case err := <-done:
		if !errors.Is(err, errRecoveryLeaseLost) {
			t.Fatalf("recoverClaimed error = %v, want lease lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("reaction reconciliation was not cancelled after lease loss")
	}
	if resumed {
		t.Fatal("Resume ran after recovery ownership was lost")
	}
	if released {
		t.Fatal("stolen recovery lease was released")
	}
}
