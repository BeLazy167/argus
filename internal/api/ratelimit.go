package api

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	repoHourlyLimit  = 10
	orgDailyLimit    = 50
	forceHourlyLimit = 3
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
