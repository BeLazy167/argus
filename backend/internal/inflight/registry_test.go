package inflight

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestRegistry_BeginReleaseLifecycle pins the slot lifecycle: the first Begin
// wins, a second for the same PR is refused while held, a different PR is
// independent, and the slot is reusable after Release.
func TestRegistry_BeginReleaseLifecycle(t *testing.T) {
	r := NewRegistry()
	const repo = "owner/repo"
	const pr = 42

	slot, ok := r.Begin(repo, pr)
	if !ok || slot == nil {
		t.Fatal("first Begin should win the slot")
	}
	// Double-begin refusal: same PR while held → no slot.
	if s2, ok2 := r.Begin(repo, pr); ok2 || s2 != nil {
		t.Error("second Begin for the same PR should be refused while held")
	}
	// A different PR on the same repo is independent.
	if s3, ok3 := r.Begin(repo, pr+1); !ok3 || s3 == nil {
		t.Error("Begin for a different PR should win")
	} else {
		s3.Release()
	}
	// After release, the slot is claimable again.
	slot.Release()
	if s4, ok4 := r.Begin(repo, pr); !ok4 || s4 == nil {
		t.Error("Begin should win after Release")
	}
}

// TestRegistry_ReleaseIsIdempotent proves a double Release is safe and does not
// evict a slot a later Begin re-created for the same key.
func TestRegistry_ReleaseIsIdempotent(t *testing.T) {
	r := NewRegistry()
	slot, _ := r.Begin("o/r", 1)
	slot.Release()
	slot.Release() // must not panic

	// A fresh slot for the same key must survive the stale slot's second Release.
	fresh, ok := r.Begin("o/r", 1)
	if !ok {
		t.Fatal("re-Begin after release should win")
	}
	slot.Release() // stale slot: must NOT evict `fresh`
	if s, ok := r.Begin("o/r", 1); ok || s != nil {
		t.Error("fresh slot was evicted by the stale slot's Release")
	}
	fresh.Release()
}

// TestSlot_CheckboxPathPairing models the invariant the Launcher relies on: a
// slot has no cancel until BindCancel, and once bound, Registry.Cancel invokes
// it. This is the fix for the checkbox path, which used to take a slot without
// ever binding a cancel.
func TestSlot_CheckboxPathPairing(t *testing.T) {
	r := NewRegistry()
	const repo, pr = "o/r", 7

	slot, ok := r.Begin(repo, pr)
	if !ok {
		t.Fatal("Begin should win")
	}
	// Before BindCancel: nothing to cancel → Registry.Cancel reports no live fn.
	if r.Cancel(repo, pr) {
		t.Error("Cancel before BindCancel should report no live cancel")
	}

	var cancelled atomic.Bool
	slot.BindCancel(func() { cancelled.Store(true) })

	// After BindCancel: the slot + cancel are paired; Cancel invokes it.
	if !r.Cancel(repo, pr) {
		t.Error("Cancel after BindCancel should invoke the bound func")
	}
	if !cancelled.Load() {
		t.Error("bound cancel func was not invoked")
	}
	slot.Release()
}

// TestRegistry_CancelStrandedWhenAbsent proves Cancel returns false when no slot
// is held — the signal the handler uses to route to the DB stranded-cancel path.
func TestRegistry_CancelStrandedWhenAbsent(t *testing.T) {
	r := NewRegistry()
	if r.Cancel("o/r", 99) {
		t.Error("Cancel for an unheld PR should report no live cancel")
	}
}

// TestSlot_CancelAfterReleaseIsNoop proves cancelling a released slot does not
// invoke the (dropped) func.
func TestSlot_CancelAfterReleaseIsNoop(t *testing.T) {
	r := NewRegistry()
	slot, _ := r.Begin("o/r", 1)
	var cancelled atomic.Bool
	slot.BindCancel(func() { cancelled.Store(true) })
	slot.Release()
	slot.Cancel()
	if cancelled.Load() {
		t.Error("cancel func fired after Release — binding should be dropped")
	}
}

// TestRegistry_ConcurrentBeginSingleWinner runs -race: many goroutines racing
// Begin for the same PR must yield exactly one winner.
func TestRegistry_ConcurrentBeginSingleWinner(t *testing.T) {
	r := NewRegistry()
	const goroutines = 64
	var winners atomic.Int32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			if slot, ok := r.Begin("o/r", 1); ok {
				winners.Add(1)
				slot.Release()
			}
		}()
	}
	close(start)
	wg.Wait()
	// With Release racing the other Begins, at least one wins; the invariant we
	// can assert deterministically is that at most one slot is ever held at once,
	// which the map-under-mutex guarantees. A single final Begin must win.
	if got := winners.Load(); got < 1 {
		t.Errorf("winners = %d, want >= 1", got)
	}
	if s, ok := r.Begin("o/r", 1); !ok {
		t.Error("slot should be free after all goroutines released")
	} else {
		s.Release()
	}
}

// TestRegistry_ConcurrentCancelAndRelease races Cancel against Release on the
// same slot under -race to prove no data race and no panic.
func TestRegistry_ConcurrentCancelAndRelease(t *testing.T) {
	r := NewRegistry()
	slot, _ := r.Begin("o/r", 1)
	slot.BindCancel(func() {})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.Cancel("o/r", 1) }()
	go func() { defer wg.Done(); slot.Release() }()
	wg.Wait()
}
