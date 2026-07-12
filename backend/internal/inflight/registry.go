// Package inflight tracks the reviews currently executing, keyed by repo + PR
// number, so a second trigger for a PR already under review is refused instead
// of double-running the pipeline (and double-posting to GitHub).
//
// It replaces the two independent sync.Maps that used to live on api.Server
// (one for the "held" marker, one for the cancel func) plus their hand-rolled
// "{repo}:{pr}" string keys. Those maps let the two facts drift: a slot could be
// held with no cancel bound (the checkbox-trigger path did exactly this), or a
// cancel could be stored for a key whose slot was never taken. Here a held slot
// and its cancel func share one lifetime by construction — Begin hands back the
// Slot, and BindCancel/Cancel/Release are the only ways to touch its cancel.
package inflight

import (
	"context"
	"fmt"
	"sync"
)

// Registry is the process-wide set of in-flight reviews. The zero value is not
// usable; construct with NewRegistry. Safe for concurrent use.
type Registry struct {
	mu    sync.Mutex
	slots map[string]*Slot
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{slots: make(map[string]*Slot)}
}

func key(repo string, pr int) string {
	return fmt.Sprintf("%s:%d", repo, pr)
}

// Begin claims the in-flight slot for repo + pr. It returns the Slot and true
// when the caller won the slot; nil and false when a review for the same PR is
// already in flight. The winner MUST eventually call Slot.Release (and normally
// Slot.BindCancel right after, pairing the slot with a cancel func).
func (r *Registry) Begin(repo string, pr int) (*Slot, bool) {
	k := key(repo, pr)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, held := r.slots[k]; held {
		return nil, false
	}
	slot := &Slot{r: r, key: k}
	r.slots[k] = slot
	return slot, true
}

// Cancel invokes the cancel func bound to the in-flight slot for repo + pr, if
// one is held and a cancel was bound. It reports whether a live cancel was
// actually found and called — false means there is no in-flight slot for this
// PR on this process (a restart, or the cancel landed on a different machine),
// so the caller should fall back to the DB-level stranded-cancel path.
func (r *Registry) Cancel(repo string, pr int) bool {
	r.mu.Lock()
	slot := r.slots[key(repo, pr)]
	r.mu.Unlock()
	if slot == nil {
		return false
	}
	return slot.invokeCancel()
}

// Slot is a held in-flight review. Its cancel func and its registry lifetime are
// paired: BindCancel attaches the cancel, Cancel invokes it, Release frees the
// slot and drops the cancel. All methods are safe for concurrent use and
// idempotent-safe against a concurrent Release.
type Slot struct {
	r   *Registry
	key string

	mu       sync.Mutex
	cancel   context.CancelFunc
	released bool
}

// BindCancel attaches the cancel func that Cancel (and Registry.Cancel) invoke.
// A no-op once the slot has been released. Binding replaces any prior func.
func (s *Slot) BindCancel(fn context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return
	}
	s.cancel = fn
}

// Cancel invokes the bound cancel func. No-op if none is bound or the slot was
// already released.
func (s *Slot) Cancel() { s.invokeCancel() }

// invokeCancel calls the bound cancel func under the slot lock's protection and
// reports whether one was actually bound.
func (s *Slot) invokeCancel() bool {
	s.mu.Lock()
	fn := s.cancel
	s.mu.Unlock()
	if fn == nil {
		return false
	}
	fn()
	return true
}

// Release frees the slot and drops its cancel binding. Idempotent — a second
// call is a no-op. After Release, a Begin for the same repo + pr can win again.
func (s *Slot) Release() {
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return
	}
	s.released = true
	s.cancel = nil
	s.mu.Unlock()

	s.r.mu.Lock()
	// Only drop the key if it still points at THIS slot — guards against a new
	// slot having claimed the key between our two locks (can't happen while we
	// hold the slot, but keeps Release correct if that ever changes).
	if s.r.slots[s.key] == s {
		delete(s.r.slots, s.key)
	}
	s.r.mu.Unlock()
}
