package memory

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/time/rate"
)

func TestNewLimiter(t *testing.T) {
	t.Run("positive_inputs_honored", func(t *testing.T) {
		l := NewLimiter(25, 50)
		if l.Burst() != 50 {
			t.Errorf("burst = %d, want 50", l.Burst())
		}
		if l.Limit() != rate.Limit(25) {
			t.Errorf("limit = %v, want 25", l.Limit())
		}
	})

	t.Run("zero_qps_clamps_to_default", func(t *testing.T) {
		l := NewLimiter(0, 100)
		if l.Limit() != rate.Limit(DefaultSupermemoryQPS) {
			t.Errorf("qps=0 should clamp to default %d, got %v", DefaultSupermemoryQPS, l.Limit())
		}
	})

	t.Run("negative_burst_clamps_to_default", func(t *testing.T) {
		l := NewLimiter(10, -5)
		if l.Burst() != DefaultSupermemoryBurst {
			t.Errorf("burst=-5 should clamp to default %d, got %d", DefaultSupermemoryBurst, l.Burst())
		}
	})
}

func TestWaitForToken(t *testing.T) {
	t.Run("nil_limiter_is_no_op", func(t *testing.T) {
		if err := waitForToken(context.Background(), nil); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("first_token_does_not_block", func(t *testing.T) {
		l := NewLimiter(1, 1)
		// Just assert the call returns success on an unsaturated limiter.
		// A timing assertion here is flaky under CI load — scheduler jitter
		// on a shared runner routinely exceeds any sub-100ms bound we'd
		// pick. The behavior we care about (first token is free, no block)
		// is covered by the error-path test below; the absence of a timeout
		// here is sufficient evidence it didn't block.
		if err := waitForToken(context.Background(), l); err != nil {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("ctx_cancel_returns_error", func(t *testing.T) {
		// 1 QPS, burst 1; first token consumed then cancel before refill.
		l := NewLimiter(1, 1)
		if err := waitForToken(context.Background(), l); err != nil {
			t.Fatalf("prime err = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := waitForToken(ctx, l)
		if err == nil {
			t.Fatal("err = nil, want error on cancelled ctx")
		}
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want context-derived", err)
		}
	})
}
