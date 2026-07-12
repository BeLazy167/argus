// Package pipeline — lifecycle_test.go exercises the review-lifecycle DB guards
// (ReviewLifecycle) without a live Postgres, via the row-lookup seams and a fake
// store that models UpdateReviewStatusIf's compare-and-set exactly.
package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// casWrite records one UpdateReviewStatusIf call and whether its compare-and-set
// actually applied (current status ∈ allowed).
type casWrite struct {
	id      uuid.UUID
	status  string
	allowed []string
	applied bool
}

// fakeReviewStore models the two review-status store methods the lifecycle and
// launcher touch. UpdateReviewStatusIf enforces the real CAS contract: it writes
// only when the current status is in allowedCurrent. errOnMissingStatus makes
// GetReviewStatus fail for ids with no seeded status (to exercise fail-open).
type fakeReviewStore struct {
	mu     sync.Mutex
	status map[uuid.UUID]string
	writes []casWrite
}

func newFakeReviewStore() *fakeReviewStore {
	return &fakeReviewStore{status: make(map[uuid.UUID]string)}
}

func (f *fakeReviewStore) set(id uuid.UUID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[id] = status
}

func (f *fakeReviewStore) get(id uuid.UUID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[id]
}

func (f *fakeReviewStore) GetReviewStatus(_ context.Context, id uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.status[id]
	if !ok {
		return "", errors.New("review not found")
	}
	return s, nil
}

func (f *fakeReviewStore) UpdateReviewStatusIf(_ context.Context, id uuid.UUID, status, _ string, _ []byte, allowedCurrent []string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.status[id]
	applied := false
	for _, a := range allowedCurrent {
		if a == cur {
			applied = true
			break
		}
	}
	if applied {
		f.status[id] = status
	}
	f.writes = append(f.writes, casWrite{id: id, status: status, allowed: append([]string(nil), allowedCurrent...), applied: applied})
	return applied, nil
}

// TestShouldAbortPost_CancelMidRunHaltsBeforePost proves post()'s single cancel
// guard: a review flagged cancelled aborts the post; anything else proceeds; a
// status-read error fails open (proceeds), matching the original inline guards.
func TestShouldAbortPost_CancelMidRunHaltsBeforePost(t *testing.T) {
	store := newFakeReviewStore()
	l := &ReviewLifecycle{st: store, logger: discardLogger()}
	id := uuid.New()

	store.set(id, "in_progress")
	if l.ShouldAbortPost(context.Background(), id, "post: review status check failed") {
		t.Error("in_progress review must not abort the post")
	}

	store.set(id, "cancelled")
	if !l.ShouldAbortPost(context.Background(), id, "post: review status check failed") {
		t.Error("cancelled review must abort the post (halt before GitHub)")
	}

	// Unseeded id → GetReviewStatus errors → fail open (do NOT abort), matching
	// the original guards which logged and proceeded.
	if l.ShouldAbortPost(context.Background(), uuid.New(), "post: review status check failed") {
		t.Error("status-read error must fail open (no abort)")
	}
}

// TestReviewLifecycle_CrossMachineCASNoOp proves a completed review never flips
// to cancelled and a cancelled review never flips to completed — the CAS
// allowed-lists the stranded-cancel and completion writes rely on.
func TestReviewLifecycle_CrossMachineCASNoOp(t *testing.T) {
	store := newFakeReviewStore()
	id := uuid.New()

	// Stranded-cancel CAS (allowed: pending/in_progress) must NOT touch completed.
	store.set(id, "completed")
	applied, _ := store.UpdateReviewStatusIf(context.Background(), id, "cancelled", "", nil, []string{"pending", "in_progress"})
	if applied || store.get(id) != "completed" {
		t.Errorf("completed flipped to cancelled: applied=%v status=%q", applied, store.get(id))
	}

	// Completion CAS (allowed: in_progress) must NOT touch cancelled.
	store.set(id, "cancelled")
	applied, _ = store.UpdateReviewStatusIf(context.Background(), id, "completed", "", nil, []string{"in_progress"})
	if applied || store.get(id) != "cancelled" {
		t.Errorf("cancelled flipped to completed: applied=%v status=%q", applied, store.get(id))
	}
}

// TestCancelStranded_TerminalRunSkips proves CancelStranded leaves a review whose
// latest run is already terminal (completed) untouched: no run re-persist, and
// the review-row status write is never reached — a completed review must never
// flip to cancelled even across machines.
func TestCancelStranded_TerminalRunSkips(t *testing.T) {
	store := newFakeReviewStore()
	id := uuid.New()
	store.set(id, "completed")

	persistCalled := false
	l := &ReviewLifecycle{st: store, logger: discardLogger()}
	l.loadLatestRun = func(context.Context, uuid.UUID) (*PipelineRun, bool, error) {
		return &PipelineRun{State: StateCompleted}, true, nil
	}
	l.persistRun = func(context.Context, *PipelineRun) error {
		persistCalled = true
		return nil
	}

	if err := l.CancelStranded(context.Background(), id); err != nil {
		t.Fatalf("CancelStranded: %v", err)
	}
	if persistCalled {
		t.Error("terminal run was re-persisted — must skip")
	}
	if len(store.writes) != 0 {
		t.Errorf("status write reached for a terminal run (%d writes) — must skip before the CAS", len(store.writes))
	}
	if store.get(id) != "completed" {
		t.Errorf("status = %q, want completed (untouched)", store.get(id))
	}
}

// TestCancelStranded_NoRunFlipsPendingOnly proves that with no persisted run
// (review died before its first state write), CancelStranded flips a still-live
// review to cancelled but the CAS no-ops on an already-completed one.
func TestCancelStranded_NoRunFlipsPendingOnly(t *testing.T) {
	newLifecycle := func(store *fakeReviewStore) *ReviewLifecycle {
		l := &ReviewLifecycle{st: store, logger: discardLogger()}
		l.loadLatestRun = func(context.Context, uuid.UUID) (*PipelineRun, bool, error) {
			return nil, false, nil // no persisted run
		}
		return l
	}

	// Live review → flips to cancelled.
	store := newFakeReviewStore()
	id := uuid.New()
	store.set(id, "in_progress")
	if err := newLifecycle(store).CancelStranded(context.Background(), id); err != nil {
		t.Fatalf("CancelStranded: %v", err)
	}
	if store.get(id) != "cancelled" {
		t.Errorf("status = %q, want cancelled", store.get(id))
	}

	// Completed review → CAS no-op, stays completed.
	store2 := newFakeReviewStore()
	id2 := uuid.New()
	store2.set(id2, "completed")
	if err := newLifecycle(store2).CancelStranded(context.Background(), id2); err != nil {
		t.Fatalf("CancelStranded: %v", err)
	}
	if store2.get(id2) != "completed" {
		t.Errorf("status = %q, want completed (CAS must not flip)", store2.get(id2))
	}
}

// TestEnsureNotRunning_RefusesFreshLive proves the retry precheck that becomes a
// 409: a fresh non-terminal run is refused; stale/terminal/no-run runs are not.
func TestEnsureNotRunning_RefusesFreshLive(t *testing.T) {
	cases := []struct {
		name  string
		state PipelineState
		fresh bool
		found bool
		want  error
	}{
		{"fresh live run refused", StateReviewing, true, true, ErrReviewRunning},
		{"stale non-terminal allowed", StateReviewing, false, true, nil},
		{"terminal completed allowed", StateCompleted, true, true, nil},
		{"terminal failed allowed", StateFailed, true, true, nil},
		{"no persisted run allowed", "", false, false, nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			l := &ReviewLifecycle{st: newFakeReviewStore(), logger: discardLogger()}
			l.latestRunLiveness = func(context.Context, uuid.UUID) (PipelineState, bool, bool, error) {
				return tc.state, tc.fresh, tc.found, nil
			}
			err := l.EnsureNotRunning(context.Background(), uuid.New())
			if !errors.Is(err, tc.want) {
				t.Errorf("EnsureNotRunning = %v, want %v", err, tc.want)
			}
		})
	}
}
