package api

import (
	"testing"

	"github.com/BeLazy167/argus/backend/internal/inflight"
)

// TestServerInflight_SamePRMutualExclusion guards the per-PR slot the launch
// paths acquire (via s.inflight) before running a review. Without mutual
// exclusion two reviews for the same PR could run concurrently and double-post.
// Exhaustive slot/cancel lifecycle coverage lives in internal/inflight; this
// test only pins the Server-held registry's mutual exclusion.
func TestServerInflight_SamePRMutualExclusion(t *testing.T) {
	s := &Server{inflight: inflight.NewRegistry()}
	const repo = "owner/repo"
	const pr = 42

	slot, ok := s.inflight.Begin(repo, pr)
	if !ok {
		t.Fatal("first acquire should succeed")
	}
	// Second acquire for the same PR must fail while the slot is held.
	if _, ok := s.inflight.Begin(repo, pr); ok {
		t.Error("second acquire for same PR should fail while slot is held")
	}
	// A different PR on the same repo is independent.
	if other, ok := s.inflight.Begin(repo, pr+1); !ok {
		t.Error("acquire for a different PR should succeed")
	} else {
		other.Release()
	}
	// After release, the slot is available again.
	slot.Release()
	if again, ok := s.inflight.Begin(repo, pr); !ok {
		t.Error("acquire should succeed after release")
	} else {
		again.Release()
	}
}
