package obs

import (
	"sync"
	"time"
)

// breakerState is the public state of the circuit breaker. Exposed as an int
// only to callers that need to branch on it; most callers use AllowRequest.
type breakerState int

const (
	stateClosed breakerState = iota
	stateOpen
	stateHalfOpen
)

// Defaults chosen to match the plan: 10 consecutive failures trip the breaker
// for 60 seconds; then a single probe request is allowed through (half-open)
// before closing or re-opening.
const (
	defaultFailureThreshold = 10
	defaultOpenDuration     = 60 * time.Second
)

// clock is the time seam used by tests. Production uses time.Now.
type clock func() time.Time

// CircuitBreaker is a thread-safe 3-state machine (closed/open/half-open).
// A breaker protects an unreliable downstream — here, PostHog's /batch
// endpoint — from receiving requests while it is clearly broken.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            breakerState
	consecutiveFails int
	openedAt         time.Time

	failureThreshold int
	openDuration     time.Duration
	now              clock
}

// NewCircuitBreaker constructs a breaker with default thresholds (10 fails, 60s).
func NewCircuitBreaker() *CircuitBreaker {
	return newCircuitBreakerWithClock(defaultFailureThreshold, defaultOpenDuration, time.Now)
}

// newCircuitBreakerWithClock is the test seam.
func newCircuitBreakerWithClock(threshold int, open time.Duration, now clock) *CircuitBreaker {
	return &CircuitBreaker{
		state:            stateClosed,
		failureThreshold: threshold,
		openDuration:     open,
		now:              now,
	}
}

// AllowRequest reports whether a caller should attempt a downstream call.
// In closed state: always true. In open state: true only once openDuration
// has elapsed (and transitions to half-open). In half-open state: false — the
// single probe permitted on transition has already been granted.
func (c *CircuitBreaker) AllowRequest() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case stateClosed:
		return true
	case stateOpen:
		if c.now().Sub(c.openedAt) >= c.openDuration {
			// transition open -> half-open; consume the one probe slot.
			c.state = stateHalfOpen
			return true
		}
		return false
	case stateHalfOpen:
		return false
	}
	return false
}

// RecordSuccess is called after a downstream call succeeds. Resets failure
// count and closes the breaker if it was half-open.
func (c *CircuitBreaker) RecordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consecutiveFails = 0
	c.state = stateClosed
}

// RecordFailure is called after a downstream call fails. In half-open state,
// any failure re-opens immediately. In closed state, 10 consecutive failures
// trip the breaker.
func (c *CircuitBreaker) RecordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == stateHalfOpen {
		c.trip()
		return
	}
	c.consecutiveFails++
	if c.consecutiveFails >= c.failureThreshold {
		c.trip()
	}
}

func (c *CircuitBreaker) trip() {
	c.state = stateOpen
	c.openedAt = c.now()
}

// IsOpen reports whether the breaker is in the open state. Half-open returns
// false because the probe slot is available for the next request.
func (c *CircuitBreaker) IsOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == stateOpen
}
