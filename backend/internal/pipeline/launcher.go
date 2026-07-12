// Package pipeline — launcher.go owns the boilerplate every review-launch path
// (webhook, manual API, slash command, checkbox, retry) used to hand-roll: take
// the per-PR in-flight slot, pair it with a cancel func, open/close the
// event-bus topic, spawn the goroutine under panic recovery, and — for a retry
// whose review row already exists — roll the review out of pending/in_progress
// limbo to failed and publish EventError when the pipeline errors.
//
// Before this, each of the five call sites repeated that dance with subtly
// different defer stacks; the checkbox path even took a slot without ever
// binding a cancel (so its reviews were un-stoppable). Routing them all through
// Launch collapses the handlers to marshaling a LaunchSpec, and makes the
// slot+cancel pairing an invariant of construction (see internal/inflight).
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/BeLazy167/argus/backend/internal/inflight"
	"github.com/BeLazy167/argus/backend/internal/obs"
	"github.com/google/uuid"
)

// ErrInFlight is returned by Launch when a review for the same repo + PR is
// already running. HTTP handlers map it to 409; GitHub-driven handlers post a
// "review already in progress" comment.
var ErrInFlight = errors.New("a review for this PR is already in flight")

// launcherStore is the narrow store surface the Launcher needs: the compare-and-
// set status write used to roll a failed retry back out of limbo. *store.Store
// satisfies it; tests substitute a fake.
type launcherStore interface {
	UpdateReviewStatusIf(ctx context.Context, id uuid.UUID, status, errMsg string, tokenUsage []byte, allowedCurrent []string) (bool, error)
}

// LaunchSpec is the immutable description of one pipeline launch. Repo + PR key
// the in-flight slot; the func fields hook path-specific work into the shared
// lifecycle.
type LaunchSpec struct {
	// Repo is the "owner/name" the in-flight slot is keyed on, alongside PR.
	Repo string
	// PR is the pull-request number.
	PR int
	// BaseCtx is the (already trace-/installation-tagged) background context the
	// launch derives its cancellable child from. Must not be a request context —
	// the goroutine outlives the request.
	BaseCtx context.Context

	// ReviewID is set ONLY for retry launches, where the review row already
	// exists and its id is known before the goroutine spawns. When set, the
	// Launcher owns the event-bus topic (open before spawn, close after) and, on
	// a non-cancel failure, rolls pending/in_progress → failed + publishes
	// EventError. Nil for HandlePREvent launches, whose review row and topic are
	// created inside the pipeline under an id the caller can't know up front.
	ReviewID *uuid.UUID

	// BeforeSpawn runs synchronously AFTER the slot is won but BEFORE the
	// goroutine spawns. Returning an error releases the slot and surfaces the
	// error from Launch WITHOUT spawning — the caller maps it (e.g. the webhook
	// semaphore-full → 503, the retry status→pending write failing → 500, the
	// checkbox rate-limit → comment). Optional.
	BeforeSpawn func(ctx context.Context) error

	// Run drives the pipeline under the cancellable, trace-tagged ctx. Required.
	Run func(ctx context.Context) error

	// OnDone is the path-specific completion hook (GitHub reaction/comment,
	// checkbox restore, failure log). Called inside the goroutine with the raw
	// result — nil on success, context.Canceled on a Stop, or the pipeline error
	// on failure — so each path can replicate its own original branching (some
	// treat a Stop as a no-op, others as failure feedback). For a failing retry
	// it runs AFTER the built-in rollback. Optional.
	OnDone func(err error)

	// Cleanup always runs on goroutine exit (webhook: release the semaphore).
	// Runs only when the goroutine actually spawned (i.e. Launch returned nil).
	// Optional.
	Cleanup func()
}

// Launcher spawns review pipelines with a shared slot/cancel/topic/rollback
// lifecycle. Construct with NewLauncher.
type Launcher struct {
	registry *inflight.Registry
	eventBus *EventBus
	st       launcherStore
	logger   *slog.Logger
}

// NewLauncher wires the launcher over the shared in-flight registry, the event
// bus, and the store. The registry MUST be the same instance the cancel path
// consults (api.Server holds it and calls registry.Cancel).
func NewLauncher(registry *inflight.Registry, eventBus *EventBus, st launcherStore, logger *slog.Logger) *Launcher {
	return &Launcher{registry: registry, eventBus: eventBus, st: st, logger: logger}
}

// Launch claims the in-flight slot for spec.Repo/PR and, on success, spawns the
// pipeline goroutine and returns nil. It returns ErrInFlight (without spawning)
// when a review for the same PR is already running, or spec.BeforeSpawn's error
// (slot released, not spawned) when post-acquire prep fails. The spawned
// goroutine owns slot release, cancel teardown, topic close, rollback, OnDone,
// Cleanup, and panic recovery.
func (l *Launcher) Launch(spec LaunchSpec) error {
	slot, ok := l.registry.Begin(spec.Repo, spec.PR)
	if !ok {
		return ErrInFlight
	}

	ctx, cancel := context.WithCancel(spec.BaseCtx)

	// Synchronous post-acquire prep. On failure, undo the acquire and surface the
	// error to the caller WITHOUT spawning — nothing to roll back, nothing ran.
	if spec.BeforeSpawn != nil {
		if err := spec.BeforeSpawn(ctx); err != nil {
			cancel()
			slot.Release()
			return err
		}
	}

	// Pair the slot with its cancel func — the invariant the two old sync.Maps
	// couldn't enforce (and the checkbox path violated).
	slot.BindCancel(cancel)

	if spec.ReviewID != nil && l.eventBus != nil {
		l.eventBus.OpenTopic(*spec.ReviewID)
	}

	go func() {
		defer func() {
			// Runs on every exit (normal, error, recovered panic). LIFO: declared
			// first → runs last, after runGuarded turned any Run panic into a
			// handled error below. The recover here is a backstop for a panic in
			// rollback/OnDone (GitHub feedback) — without it that would crash the
			// whole process, killing every other in-flight review.
			if r := recover(); r != nil {
				l.logger.Error("launch goroutine panic (post-run)",
					"recover", r, "repo", spec.Repo, "pr", spec.PR, "stack", string(debug.Stack()))
				emitPipelinePanicEvent(ctx, l.logger, "launcher", r, obs.TraceID(ctx))
			}
			if spec.ReviewID != nil && l.eventBus != nil {
				l.eventBus.CloseTopic(*spec.ReviewID)
			}
			if spec.Cleanup != nil {
				spec.Cleanup()
			}
			cancel()
			slot.Release()
		}()

		err := l.runGuarded(ctx, spec)
		// Roll a failed retry out of pending/in_progress limbo — but never on a
		// Stop (context.Canceled): the state machine already wrote the cancelled
		// status, and the original retry path guarded its rollback the same way.
		if err != nil && !errors.Is(err, context.Canceled) {
			l.rollback(ctx, spec, err)
		}
		// OnDone gets the raw result so each path replicates its original
		// branching (a Stop is a no-op for some paths, failure feedback for others).
		if spec.OnDone != nil {
			spec.OnDone(err)
		}
	}()

	return nil
}

// runGuarded runs the pipeline and converts a panic into an error so the
// goroutine's cleanup + rollback still run. Previously an unrecovered panic in a
// launch goroutine crashed the whole process (killing every other in-flight
// review); recovering here is strictly safer.
func (l *Launcher) runGuarded(ctx context.Context, spec LaunchSpec) (err error) {
	defer func() {
		if r := recover(); r != nil {
			l.logger.Error("launch goroutine panic",
				"recover", r, "repo", spec.Repo, "pr", spec.PR, "stack", string(debug.Stack()))
			emitPipelinePanicEvent(ctx, l.logger, "launcher", r, obs.TraceID(ctx))
			err = fmt.Errorf("pipeline panic: %v", r)
		}
	}()
	return spec.Run(ctx)
}

// rollback rolls a failed launch's review out of pending/in_progress limbo to
// failed and publishes EventError — but ONLY for launches that own a known
// review id (retry). HandlePREvent launches create + own their review row
// inside the pipeline, so the state machine already wrote the terminal status;
// there is no external row for the Launcher to touch.
func (l *Launcher) rollback(ctx context.Context, spec LaunchSpec, cause error) {
	if spec.ReviewID == nil {
		return
	}
	id := *spec.ReviewID
	// Detached from the launch ctx (about to be cancelled by the deferred
	// teardown) but keeps trace attribution. Conditional so a Stop that raced
	// this failure isn't flipped from cancelled back to failed.
	if _, uerr := l.st.UpdateReviewStatusIf(context.WithoutCancel(ctx), id, "failed", cause.Error(), nil, []string{"pending", "in_progress"}); uerr != nil {
		l.logger.Error("launch: failed to roll back review status", "error", uerr, "review_id", id)
	}
	if l.eventBus != nil {
		l.eventBus.Publish(id, EventError, map[string]string{"error": cause.Error()})
	}
}
