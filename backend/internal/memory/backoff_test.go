package memory

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// testPolicy is fast enough to keep table-driven tests under ~100ms even with
// the full 3 attempts firing. Keeps jitter at zero so timing assertions are
// deterministic.
var testPolicy = BackoffPolicy{
	MaxAttempts:  3,
	InitialDelay: 2 * time.Millisecond,
	MaxDelay:     8 * time.Millisecond,
	Multiplier:   2.0,
	Jitter:       0,
}

func TestRetryWithBackoff(t *testing.T) {
	t.Run("success_first_try", func(t *testing.T) {
		calls := 0
		err := retryWithBackoff(context.Background(), testPolicy, func(ctx context.Context) error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1", calls)
		}
	})

	t.Run("retryable_recovers_on_second", func(t *testing.T) {
		calls := 0
		err := retryWithBackoff(context.Background(), testPolicy, func(ctx context.Context) error {
			calls++
			if calls == 1 {
				return &retryableError{StatusCode: 503, Body: []byte("unavailable")}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if calls != 2 {
			t.Errorf("calls = %d, want 2", calls)
		}
	})

	t.Run("exhausts_and_returns_last_err", func(t *testing.T) {
		calls := 0
		err := retryWithBackoff(context.Background(), testPolicy, func(ctx context.Context) error {
			calls++
			return &retryableError{StatusCode: 429, Body: []byte("rate limited")}
		})
		if err == nil {
			t.Fatal("err = nil, want retryable")
		}
		var re *retryableError
		if !errors.As(err, &re) {
			t.Errorf("final err is not *retryableError: %T", err)
		}
		if calls != testPolicy.MaxAttempts {
			t.Errorf("calls = %d, want %d", calls, testPolicy.MaxAttempts)
		}
	})

	t.Run("non_retryable_short_circuits", func(t *testing.T) {
		calls := 0
		bad := fmt.Errorf("invalid request")
		err := retryWithBackoff(context.Background(), testPolicy, func(ctx context.Context) error {
			calls++
			return bad
		})
		if !errors.Is(err, bad) {
			t.Errorf("err = %v, want %v", err, bad)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1 (non-retryable must short-circuit)", calls)
		}
	})

	t.Run("context_cancel_stops_retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := retryWithBackoff(ctx, testPolicy, func(ctx context.Context) error {
			calls++
			cancel() // cancel during the first attempt's error handling
			return &retryableError{StatusCode: 503}
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1 (cancel must stop further attempts)", calls)
		}
	})

	t.Run("retry_after_honors_server_hint", func(t *testing.T) {
		calls := 0
		start := time.Now()
		// Use a Retry-After bigger than the exponential delay to prove the
		// policy takes the max of the two.
		fastPolicy := testPolicy
		fastPolicy.InitialDelay = 1 * time.Millisecond
		err := retryWithBackoff(context.Background(), fastPolicy, func(ctx context.Context) error {
			calls++
			if calls == 1 {
				return &retryableError{StatusCode: 429, RetryAfter: 20 * time.Millisecond}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		elapsed := time.Since(start)
		if elapsed < 18*time.Millisecond {
			t.Errorf("elapsed = %v, want >= 18ms (Retry-After should have delayed)", elapsed)
		}
	})
}

// TestRetryWithBackoff_CumulativeWaitCap pins idx 12: a run of honored
// Retry-After hints must not exceed the cumulative wait ceiling. Each attempt
// returns a Retry-After larger than the whole (test-shrunk) budget, so the loop
// clamps the first sleep to the budget then gives up — it must NOT sleep once
// per attempt for the full hint.
func TestRetryWithBackoff_CumulativeWaitCap(t *testing.T) {
	orig := MaxCumulativeRetryWait
	MaxCumulativeRetryWait = 10 * time.Millisecond
	defer func() { MaxCumulativeRetryWait = orig }()

	policy := BackoffPolicy{MaxAttempts: 50, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond, Multiplier: 1}
	calls := 0
	start := time.Now()
	err := retryWithBackoff(context.Background(), policy, func(ctx context.Context) error {
		calls++
		return &retryableError{StatusCode: 429, RetryAfter: 500 * time.Millisecond}
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("want retryable error after cap exhausted")
	}
	// First sleep clamps 500ms -> 10ms budget; budget then spent, loop breaks.
	// Two fn() calls total (initial + one retry), far below MaxAttempts=50.
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (cumulative cap stops retries early)", calls)
	}
	// Total honored sleep must be bounded by the cap, nowhere near 50×500ms.
	if elapsed > 100*time.Millisecond {
		t.Errorf("elapsed = %v, want bounded by cumulative cap ~10ms", elapsed)
	}
}

func TestIsRetryableStatus(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{200, false},
		{400, false},
		{401, false},
		{404, false},
		{429, true},
		{500, false}, // app-level errors don't benefit from retry
		{502, true},
		{503, true},
		{504, true},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status_%d", tc.code), func(t *testing.T) {
			if got := isRetryableStatus(tc.code); got != tc.want {
				t.Errorf("isRetryableStatus(%d) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"5", 5 * time.Second},
		{"not a number", 0},
		{"  7  ", 7 * time.Second},
		// Strict integer: lossy floats and scientific notation must NOT
		// silently under-delay. fmt.Sscanf would have returned 0 and 1.
		{"0.5", 0},
		{"1e2", 0},
		{"10s", 0},
		// Cap applies to oversized hints so a misconfigured server cannot
		// stall the pipeline for an hour.
		{"3600", MaxRetryAfter},
		// Overflow guard: time.Duration is int64 nanoseconds. Multiplying
		// an enormous secs value by time.Second (1e9) would wrap negative
		// and skip the retry wait entirely. The cap-before-multiply path
		// must still return MaxRetryAfter here.
		{"99999999999", MaxRetryAfter},
		{"-5", 0},
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.in); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
