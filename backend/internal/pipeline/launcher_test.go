// Package pipeline — launcher_test.go exercises the shared launch lifecycle
// (slot acquire → cancel pairing → spawn → rollback) without a live Postgres,
// via a real in-flight registry, a real event bus, and the fake review store.
package pipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/inflight"
	"github.com/google/uuid"
)

func newTestLauncher(store *fakeReviewStore) (*Launcher, *inflight.Registry, *EventBus) {
	reg := inflight.NewRegistry()
	bus := NewEventBus()
	return NewLauncher(reg, bus, store, discardLogger()), reg, bus
}

// waitFor blocks until done is closed or the deadline elapses. Launch spawns a
// goroutine; tests sync on its OnDone (which runs after any rollback).
func waitFor(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("launch goroutine did not finish in time")
	}
}

// waitForSlotFree polls until repo/pr's slot frees. slot.Release runs in the
// launch goroutine's deferred cleanup AFTER OnDone, so a test that synced on
// OnDone must poll — a one-shot Begin races the deferred Release.
func waitForSlotFree(t *testing.T, reg *inflight.Registry, repo string, pr int) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if slot, ok := reg.Begin(repo, pr); ok {
			slot.Release()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("slot for %s#%d not released", repo, pr)
}

// TestLaunch_RetryRollbackOnError proves a failing retry launch (ReviewID set)
// is rolled out of pending → failed (no stranded pending) and publishes
// EventError — the machinery the retry handler used to own inline.
func TestLaunch_RetryRollbackOnError(t *testing.T) {
	store := newFakeReviewStore()
	l, _, bus := newTestLauncher(store)

	id := uuid.New()
	store.set(id, "pending") // BeforeSpawn would set this; seed it directly.

	var gotErrEvent bool
	var mu sync.Mutex
	bus.SubscribeGlobal(func(reviewID uuid.UUID, evt Event) {
		if reviewID == id && evt.Type == EventError {
			mu.Lock()
			gotErrEvent = true
			mu.Unlock()
		}
	})

	done := make(chan struct{})
	boom := errors.New("stage reviewing failed")
	err := l.Launch(LaunchSpec{
		Repo:     "o/r",
		PR:       1,
		BaseCtx:  context.Background(),
		ReviewID: &id,
		Run:      func(context.Context) error { return boom },
		OnDone:   func(error) { close(done) },
	})
	if err != nil {
		t.Fatalf("Launch returned %v, want nil", err)
	}
	waitFor(t, done)

	if got := store.get(id); got != "failed" {
		t.Errorf("status = %q, want failed (no stranded pending)", got)
	}
	// The rollback write must be the conditional CAS (pending/in_progress only).
	if len(store.writes) != 1 {
		t.Fatalf("status writes = %d, want 1", len(store.writes))
	}
	w := store.writes[0]
	if w.status != "failed" || !equalStrings(w.allowed, []string{"pending", "in_progress"}) {
		t.Errorf("rollback write = %+v, want failed with allowed [pending in_progress]", w)
	}
	mu.Lock()
	defer mu.Unlock()
	if !gotErrEvent {
		t.Error("EventError was not published on retry failure")
	}
}

// TestLaunch_NoRollbackForHandlePREventPath proves a failing HandlePREvent launch
// (ReviewID nil — the review row is owned inside the pipeline) does NOT trigger a
// launcher-level rollback: the state machine already wrote the terminal status.
func TestLaunch_NoRollbackForHandlePREventPath(t *testing.T) {
	store := newFakeReviewStore()
	l, _, _ := newTestLauncher(store)

	done := make(chan struct{})
	err := l.Launch(LaunchSpec{
		Repo:    "o/r",
		PR:      2,
		BaseCtx: context.Background(),
		Run:     func(context.Context) error { return errors.New("boom") },
		OnDone:  func(error) { close(done) },
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	waitFor(t, done)

	if len(store.writes) != 0 {
		t.Errorf("HandlePREvent-path failure triggered %d status writes — the launcher must not roll back a pipeline-owned review", len(store.writes))
	}
}

// TestLaunch_InFlightRefusesSecond proves a second Launch for the same PR is
// refused with ErrInFlight while the first is running, and succeeds once it
// releases the slot.
func TestLaunch_InFlightRefusesSecond(t *testing.T) {
	store := newFakeReviewStore()
	l, _, _ := newTestLauncher(store)

	release := make(chan struct{})
	first := make(chan struct{})
	if err := l.Launch(LaunchSpec{
		Repo:    "o/r",
		PR:      3,
		BaseCtx: context.Background(),
		Run: func(context.Context) error {
			close(first)
			<-release // hold the slot until the test lets go
			return nil
		},
	}); err != nil {
		t.Fatalf("first Launch: %v", err)
	}
	<-first // slot is now held

	if err := l.Launch(LaunchSpec{Repo: "o/r", PR: 3, BaseCtx: context.Background(), Run: func(context.Context) error { return nil }}); !errors.Is(err, ErrInFlight) {
		t.Errorf("second Launch = %v, want ErrInFlight", err)
	}

	// Release the first; the slot frees and a fresh Launch wins.
	done := make(chan struct{})
	close(release)
	// Poll until the slot frees (first goroutine's deferred Release).
	var got error
	for i := 0; i < 200; i++ {
		if got = l.Launch(LaunchSpec{Repo: "o/r", PR: 3, BaseCtx: context.Background(), Run: func(context.Context) error { return nil }, OnDone: func(error) { close(done) }}); got == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got != nil {
		t.Fatalf("Launch after release = %v, want nil (slot should free)", got)
	}
	waitFor(t, done)
}

// TestLaunch_BeforeSpawnErrorReleasesSlot proves a BeforeSpawn error releases the
// slot and surfaces the error WITHOUT spawning — the pattern the webhook (sem
// full → 503), retry (mark-pending fail → 500), and checkbox (rate limit) use.
func TestLaunch_BeforeSpawnErrorReleasesSlot(t *testing.T) {
	store := newFakeReviewStore()
	l, reg, _ := newTestLauncher(store)

	sentinel := errors.New("pre-check failed")
	ran := false
	err := l.Launch(LaunchSpec{
		Repo:        "o/r",
		PR:          4,
		BaseCtx:     context.Background(),
		BeforeSpawn: func(context.Context) error { return sentinel },
		Run:         func(context.Context) error { ran = true; return nil },
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("Launch = %v, want the BeforeSpawn sentinel", err)
	}
	if ran {
		t.Error("Run executed despite BeforeSpawn error — must not spawn")
	}
	// Slot must be free again.
	if slot, ok := reg.Begin("o/r", 4); !ok {
		t.Error("slot not released after BeforeSpawn error")
	} else {
		slot.Release()
	}
}

// TestLaunch_RunPanicRollsBackAndReleases proves a panic inside Run is recovered
// (no process crash), drives the retry rollback (pending → failed), and frees the
// slot — the backstop the raw launch goroutines never had.
func TestLaunch_RunPanicRollsBackAndReleases(t *testing.T) {
	store := newFakeReviewStore()
	l, reg, _ := newTestLauncher(store)
	id := uuid.New()
	store.set(id, "pending")

	done := make(chan struct{})
	if err := l.Launch(LaunchSpec{
		Repo:     "o/r",
		PR:       6,
		BaseCtx:  context.Background(),
		ReviewID: &id,
		Run:      func(context.Context) error { panic("kaboom") },
		OnDone:   func(error) { close(done) },
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	waitFor(t, done)

	if got := store.get(id); got != "failed" {
		t.Errorf("status = %q, want failed (panic must roll back the retry)", got)
	}
	waitForSlotFree(t, reg, "o/r", 6)
}

// TestLaunch_OnDonePanicDoesNotLeakSlot proves a panic in OnDone (path feedback)
// is recovered by the goroutine's backstop and the slot is still released.
func TestLaunch_OnDonePanicDoesNotLeakSlot(t *testing.T) {
	store := newFakeReviewStore()
	l, reg, _ := newTestLauncher(store)

	panicked := make(chan struct{})
	if err := l.Launch(LaunchSpec{
		Repo:    "o/r",
		PR:      7,
		BaseCtx: context.Background(),
		Run:     func(context.Context) error { return nil },
		OnDone: func(error) {
			close(panicked)
			panic("feedback boom")
		},
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	<-panicked
	waitForSlotFree(t, reg, "o/r", 7)
}

// TestLaunch_CancelPropagatesToRun proves the launcher binds the slot's cancel so
// Registry.Cancel (the cancel handler's path) aborts the running pipeline's ctx.
func TestLaunch_CancelPropagatesToRun(t *testing.T) {
	store := newFakeReviewStore()
	l, reg, _ := newTestLauncher(store)

	started := make(chan struct{})
	done := make(chan struct{})
	var runErr error
	if err := l.Launch(LaunchSpec{
		Repo:    "o/r",
		PR:      5,
		BaseCtx: context.Background(),
		Run: func(ctx context.Context) error {
			close(started)
			<-ctx.Done() // wait for the cancel to land
			runErr = ctx.Err()
			return ctx.Err()
		},
		OnDone: func(error) { close(done) },
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	<-started
	if !reg.Cancel("o/r", 5) {
		t.Fatal("Registry.Cancel reported no live cancel — slot/cancel not paired")
	}
	waitFor(t, done)
	if !errors.Is(runErr, context.Canceled) {
		t.Errorf("Run ctx err = %v, want context.Canceled", runErr)
	}
}
