package obs

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a thread-safe monotonic clock for deterministic breaker tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestCircuitBreakerTransitions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(t *testing.T, cb *CircuitBreaker, fc *fakeClock)
	}{
		{
			name: "starts closed and allows requests",
			run: func(t *testing.T, cb *CircuitBreaker, _ *fakeClock) {
				if !cb.AllowRequest() {
					t.Fatalf("closed breaker must allow requests")
				}
				if cb.IsOpen() {
					t.Fatalf("freshly constructed breaker should not report open")
				}
			},
		},
		{
			name: "trips after threshold failures",
			run: func(t *testing.T, cb *CircuitBreaker, _ *fakeClock) {
				for i := 0; i < 10; i++ {
					cb.RecordFailure()
				}
				if !cb.IsOpen() {
					t.Fatalf("expected breaker to be open after 10 failures")
				}
				if cb.AllowRequest() {
					t.Fatalf("open breaker must not allow requests within window")
				}
			},
		},
		{
			name: "half-opens after window and closes on success",
			run: func(t *testing.T, cb *CircuitBreaker, fc *fakeClock) {
				for i := 0; i < 10; i++ {
					cb.RecordFailure()
				}
				fc.advance(60 * time.Second)
				if !cb.AllowRequest() {
					t.Fatalf("expected probe slot after 60s")
				}
				// Further AllowRequest while half-open must be denied.
				if cb.AllowRequest() {
					t.Fatalf("half-open breaker must only grant one probe")
				}
				cb.RecordSuccess()
				if cb.IsOpen() {
					t.Fatalf("success should close breaker")
				}
				if !cb.AllowRequest() {
					t.Fatalf("closed breaker must allow requests")
				}
			},
		},
		{
			name: "half-open failure re-opens immediately",
			run: func(t *testing.T, cb *CircuitBreaker, fc *fakeClock) {
				for i := 0; i < 10; i++ {
					cb.RecordFailure()
				}
				fc.advance(60 * time.Second)
				cb.AllowRequest() // grant probe, transition half-open
				cb.RecordFailure()
				if !cb.IsOpen() {
					t.Fatalf("half-open failure must re-open breaker")
				}
				// openedAt resets, so window must elapse again.
				fc.advance(59 * time.Second)
				if cb.AllowRequest() {
					t.Fatalf("breaker must still be open before new 60s elapse")
				}
			},
		},
		{
			name: "success resets consecutive-failure counter",
			run: func(t *testing.T, cb *CircuitBreaker, _ *fakeClock) {
				for i := 0; i < 9; i++ {
					cb.RecordFailure()
				}
				cb.RecordSuccess()
				for i := 0; i < 9; i++ {
					cb.RecordFailure()
				}
				if cb.IsOpen() {
					t.Fatalf("breaker should not trip — counter was reset")
				}
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fc := &fakeClock{t: time.Unix(0, 0)}
			cb := newCircuitBreakerWithClock(10, 60*time.Second, fc.now)
			tt.run(t, cb, fc)
		})
	}
}

// TestCircuitBreakerConcurrent asserts race-free behaviour under contention.
// Run with -race to catch mutex misuse.
func TestCircuitBreakerConcurrent(t *testing.T) {
	t.Parallel()
	cb := NewCircuitBreaker()
	var wg sync.WaitGroup
	var calls atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10_000; j++ {
				if cb.AllowRequest() {
					calls.Add(1)
				}
				if j%3 == 0 {
					cb.RecordFailure()
				} else {
					cb.RecordSuccess()
				}
			}
		}()
	}
	wg.Wait()
	if calls.Load() == 0 {
		t.Fatalf("expected some requests to be allowed")
	}
}
