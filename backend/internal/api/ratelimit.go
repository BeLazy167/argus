package api

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	repoHourlyLimit  = 30
	orgDailyLimit    = 200
	forceHourlyLimit = 10
)

// RateLimiter manages per-repo and per-org rate limits using token buckets.
type RateLimiter struct {
	repoHourly  sync.Map // repo full name → *rate.Limiter (burst 10, refill ~10/hr)
	orgDaily    sync.Map // org login → *rate.Limiter (burst 50, refill ~50/day)
	forceHourly sync.Map // repo full name → *rate.Limiter (burst 3, refill ~3/hr)
	stopOnce    sync.Once
	done        chan struct{}
}

func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{done: make(chan struct{})}
	go rl.pruneLoop()
	return rl
}

// AllowReview reserves tokens from all applicable limiters atomically.
// When force=true, an additional per-repo force budget is also required.
// If any limiter denies, all reservations are cancelled to prevent token leak.
func (rl *RateLimiter) AllowReview(repoFullName, orgLogin string, force bool) bool {
	now := time.Now()

	orgLim := rl.getOrCreate(&rl.orgDaily, orgLogin, rate.Every(24*time.Hour/orgDailyLimit), orgDailyLimit)
	repoLim := rl.getOrCreate(&rl.repoHourly, repoFullName, rate.Every(time.Hour/repoHourlyLimit), repoHourlyLimit)

	orgR := orgLim.ReserveN(now, 1)
	repoR := repoLim.ReserveN(now, 1)

	var forceR *rate.Reservation
	if force {
		forceLim := rl.getOrCreate(&rl.forceHourly, repoFullName, rate.Every(time.Hour/forceHourlyLimit), forceHourlyLimit)
		forceR = forceLim.ReserveN(now, 1)
	}

	ok := orgR.DelayFrom(now) == 0 && repoR.DelayFrom(now) == 0
	if force && forceR != nil {
		ok = ok && forceR.DelayFrom(now) == 0
	}

	if !ok {
		orgR.CancelAt(now)
		repoR.CancelAt(now)
		if forceR != nil {
			forceR.CancelAt(now)
		}
	}
	return ok
}

func (rl *RateLimiter) getOrCreate(m *sync.Map, key string, r rate.Limit, burst int) *rate.Limiter {
	if v, ok := m.Load(key); ok {
		return v.(*rate.Limiter)
	}
	lim := rate.NewLimiter(r, burst)
	actual, _ := m.LoadOrStore(key, lim)
	return actual.(*rate.Limiter)
}

// pruneLoop clears hourly limiter entries every hour to prevent unbounded memory growth.
// orgDaily is NOT pruned here — its token bucket naturally enforces the 24h window.
func (rl *RateLimiter) pruneLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.clearMap(&rl.repoHourly)
			rl.clearMap(&rl.forceHourly)
		case <-rl.done:
			return
		}
	}
}

func (rl *RateLimiter) clearMap(m *sync.Map) {
	m.Range(func(key, _ any) bool {
		m.Delete(key)
		return true
	})
}

func (rl *RateLimiter) Stop() {
	rl.stopOnce.Do(func() { close(rl.done) })
}

// allowReview applies plan-aware rate limiting. Pro-tier installations bypass
// the per-repo/per-org/force buckets entirely; Free-tier falls through to the
// underlying token-bucket limiter.
//
// A failed plan lookup is treated as Free-tier (fail-safe), and only logged at
// Warn level — we never want a DB blip to silently uncap a Free install.
//
// Args:
//
//	ctx: request context; lookup is bounded by the caller's deadline.
//	repoFullName: "owner/repo" used as the per-repo bucket key.
//	orgLogin: GitHub org/user login used as the per-org bucket key.
//	force: passes through to the underlying limiter's force-bucket logic.
//	ghInstallationID: GitHub installation ID (not the internal store row ID).
//
// Returns true if the review is allowed to proceed.
func (s *Server) allowReview(ctx context.Context, repoFullName, orgLogin string, force bool, ghInstallationID int64) bool {
	inst, err := s.store.GetInstallationByGitHubID(ctx, ghInstallationID)
	if err != nil {
		s.logger.Warn("rate limit: plan lookup failed, applying free-tier caps", "error", err, "ghInstallationID", ghInstallationID)
	} else if s.cfg.IsPro(inst.PlanTier) {
		return true
	}
	return s.rateLimiter.AllowReview(repoFullName, orgLogin, force)
}
