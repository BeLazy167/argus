package api

import "testing"

// TestTryAcquireReview_SamePRMutualExclusion guards the per-PR slot the retry
// handler now acquires (like the webhook path) before flipping status and
// storing its cancel fn. Without mutual exclusion, a retry could run
// concurrently with a live review for the same PR and clobber its shared-key
// cancel fn. DB-less: the slot is an in-memory sync.Map on Server.
func TestTryAcquireReview_SamePRMutualExclusion(t *testing.T) {
	s := &Server{}
	const repo = "owner/repo"
	const pr = 42

	if !s.tryAcquireReview(repo, pr) {
		t.Fatal("first acquire should succeed")
	}
	// Second acquire for the same PR must fail while the slot is held.
	if s.tryAcquireReview(repo, pr) {
		t.Error("second acquire for same PR should fail while slot is held")
	}
	// A different PR on the same repo is independent.
	if !s.tryAcquireReview(repo, pr+1) {
		t.Error("acquire for a different PR should succeed")
	}
	// After release, the slot is available again.
	s.releaseReview(repo, pr)
	if !s.tryAcquireReview(repo, pr) {
		t.Error("acquire should succeed after release")
	}
}
