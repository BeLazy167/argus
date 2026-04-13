package api

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestRateLimitNewRateLimiter(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	if rl.done == nil {
		t.Fatal("done channel not initialized")
	}
}

func TestRateLimitAllowUnderLimit(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	// First request for a fresh repo/org should always be allowed
	if !rl.AllowReview("owner/repo", "owner", false) {
		t.Fatal("first request should be allowed")
	}
}

func TestRateLimitRepoHourlyExhaustion(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	repo := "owner/repo-exhaust"
	org := "owner-exhaust"

	// Exhaust the repo hourly burst (10 requests)
	for i := 0; i < repoHourlyLimit; i++ {
		if !rl.AllowReview(repo, org, false) {
			t.Fatalf("request %d should be allowed (under repo limit)", i+1)
		}
	}

	// Next request should be blocked
	if rl.AllowReview(repo, org, false) {
		t.Fatal("request beyond repo hourly limit should be blocked")
	}
}

func TestRateLimitOrgDailyExhaustion(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	org := "org-exhaust"

	// Exhaust the org daily burst (50 requests) using different repos
	for i := 0; i < orgDailyLimit; i++ {
		repo := fmt.Sprintf("org-exhaust/repo-%d", i)
		if !rl.AllowReview(repo, org, false) {
			t.Fatalf("request %d should be allowed (under org limit)", i+1)
		}
	}

	// Any new repo under the same org should be blocked
	if rl.AllowReview("org-exhaust/repo-new", org, false) {
		t.Fatal("request beyond org daily limit should be blocked")
	}
}

func TestRateLimitForceExhaustion(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	repo := "owner/repo-force"
	org := "owner-force"

	// Exhaust the force hourly burst (3 requests)
	for i := 0; i < forceHourlyLimit; i++ {
		if !rl.AllowReview(repo, org, true) {
			t.Fatalf("force request %d should be allowed", i+1)
		}
	}

	// Force request should be blocked, but non-force should still work
	if rl.AllowReview(repo, org, true) {
		t.Fatal("force request beyond limit should be blocked")
	}
	if !rl.AllowReview(repo, org, false) {
		t.Fatal("non-force request should still be allowed after force exhaustion")
	}
}

func TestRateLimitWindowExpiry(t *testing.T) {
	rl := &RateLimiter{done: make(chan struct{})}
	defer rl.Stop()

	// Inject a fast-refilling limiter: burst 1, refill every 50ms
	key := "owner/fast-repo"
	fastLim := rate.NewLimiter(rate.Every(50*time.Millisecond), 1)
	rl.repoHourly.Store(key, fastLim)

	// Also need an org limiter with plenty of capacity
	orgLim := rate.NewLimiter(rate.Limit(1000), 1000)
	rl.orgDaily.Store("owner-fast", orgLim)

	if !rl.AllowReview(key, "owner-fast", false) {
		t.Fatal("first request should be allowed")
	}
	if rl.AllowReview(key, "owner-fast", false) {
		t.Fatal("second request should be blocked (burst=1)")
	}

	// Wait for token refill
	time.Sleep(80 * time.Millisecond)

	if !rl.AllowReview(key, "owner-fast", false) {
		t.Fatal("request after window expiry should be allowed")
	}
}

func TestRateLimitDifferentKeys(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	// Exhaust repo A
	for i := 0; i < repoHourlyLimit; i++ {
		rl.AllowReview("owner/repo-a", "owner-keys", false)
	}

	// Repo A should be blocked
	if rl.AllowReview("owner/repo-a", "owner-keys", false) {
		t.Fatal("repo-a should be blocked after exhaustion")
	}

	// Repo B under the same org should still be allowed
	if !rl.AllowReview("owner/repo-b", "owner-keys", false) {
		t.Fatal("repo-b should be allowed (separate repo limiter)")
	}
}

func TestRateLimitDifferentOrgs(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	// Exhaust org A
	for i := 0; i < orgDailyLimit; i++ {
		rl.AllowReview(fmt.Sprintf("orgA/repo-%d", i), "orgA", false)
	}

	if rl.AllowReview("orgA/repo-new", "orgA", false) {
		t.Fatal("orgA should be blocked")
	}

	// Org B should still be allowed
	if !rl.AllowReview("orgB/repo-1", "orgB", false) {
		t.Fatal("orgB should be allowed (separate org limiter)")
	}
}

func TestRateLimitConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()

	var wg sync.WaitGroup
	const goroutines = 50

	// All goroutines hit the same repo/org concurrently
	allowed := make([]bool, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			allowed[idx] = rl.AllowReview("owner/repo-concurrent", "owner-concurrent", false)
		}(i)
	}
	wg.Wait()

	// Count how many were allowed — should not exceed the burst limit
	count := 0
	for _, ok := range allowed {
		if ok {
			count++
		}
	}

	if count > repoHourlyLimit {
		t.Fatalf("allowed %d requests, expected at most %d (repo burst)", count, repoHourlyLimit)
	}
	if count == 0 {
		t.Fatal("expected at least one request to be allowed")
	}
}

func TestRateLimitAtomicCancellation(t *testing.T) {
	rl := &RateLimiter{done: make(chan struct{})}
	defer rl.Stop()

	repo := "owner/repo-atomic"
	org := "org-atomic"

	// Give org a burst of 1, repo a burst of 10
	orgLim := rate.NewLimiter(rate.Every(time.Hour), 1)
	rl.orgDaily.Store(org, orgLim)
	repoLim := rate.NewLimiter(rate.Every(time.Hour/repoHourlyLimit), repoHourlyLimit)
	rl.repoHourly.Store(repo, repoLim)

	// First call should succeed (both have tokens)
	if !rl.AllowReview(repo, org, false) {
		t.Fatal("first request should be allowed")
	}

	// Second call: org is exhausted, so it should fail.
	// Repo tokens should also be restored (cancellation).
	if rl.AllowReview(repo, org, false) {
		t.Fatal("second request should be blocked (org exhausted)")
	}

	// Verify repo still has tokens by using a different org with plenty of budget
	freshOrg := "org-atomic-fresh"
	freshOrgLim := rate.NewLimiter(rate.Limit(1000), 1000)
	rl.orgDaily.Store(freshOrg, freshOrgLim)

	// Should succeed since repo tokens were cancelled back
	if !rl.AllowReview(repo, freshOrg, false) {
		t.Fatal("repo tokens should have been restored after atomic cancellation")
	}
}

func TestRateLimitStop(t *testing.T) {
	rl := NewRateLimiter()

	// Calling Stop twice should not panic
	rl.Stop()
	rl.Stop()
}
