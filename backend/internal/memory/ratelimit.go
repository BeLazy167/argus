package memory

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/time/rate"
)

// Per-installation Supermemory QPS defaults. A 40-file deep review issues
// roughly 120 searches (3 reads per file × 40 files); at 50 QPS sustained
// with a 100-burst bucket, that run clears in ≈2.5s while leaving headroom
// for 3–4 overlapping PRs before the limiter blocks.
const (
	DefaultSupermemoryQPS   = 50
	DefaultSupermemoryBurst = 100
)

// MaxWaitForToken caps how long any single HTTP call will block on the rate
// limiter. Higher than this means the caller is better off failing fast so
// the pipeline doesn't stall behind a saturated bucket.
const MaxWaitForToken = 5 * time.Second

// NewLimiter constructs a token bucket with the given QPS + burst. Both
// parameters must be positive; NewLimiter clamps non-positive values to
// DefaultSupermemoryQPS/DefaultSupermemoryBurst so callers cannot accidentally
// disable rate limiting by passing zero.
func NewLimiter(qps, burst int) *rate.Limiter {
	if qps <= 0 {
		qps = DefaultSupermemoryQPS
	}
	if burst <= 0 {
		burst = DefaultSupermemoryBurst
	}
	return rate.NewLimiter(rate.Limit(qps), burst)
}

// waitForToken blocks until the limiter grants a token or the derived wait
// budget expires. Passing a nil limiter is a no-op (returns immediately) so
// test doubles and the zero-value installation can skip rate limiting.
//
// The internal wait context caps blocking at MaxWaitForToken even if the
// caller's ctx is longer — a saturated bucket should surface as an error
// quickly, not tie up a pipeline goroutine.
func waitForToken(ctx context.Context, limiter *rate.Limiter) error {
	if limiter == nil {
		return nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, MaxWaitForToken)
	defer cancel()
	if err := limiter.Wait(waitCtx); err != nil {
		return fmt.Errorf("supermemory rate limiter: %w", err)
	}
	return nil
}
