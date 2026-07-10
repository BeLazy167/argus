package memory

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// BackoffPolicy controls exponential-backoff retry behavior for the Supermemory
// HTTP client. Delays grow by Multiplier each attempt, capped at MaxDelay,
// with ±Jitter uniform randomness per sleep to avoid thundering herds.
//
// DefaultBackoff is tuned so the total retry budget fits inside the 5-second
// search context used by SpecialistBlock / SearchPatternMatch: 250ms + 500ms +
// 1s = 1.75s worst case, leaving ~3s for actual API calls.
type BackoffPolicy struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Multiplier   float64
	Jitter       time.Duration // max absolute jitter each way (±Jitter)
}

// DefaultBackoff is applied uniformly to reads and writes. Writes that fail
// after exhausting retries are recovered by the reconciler (cmd/reconcile-memory),
// which re-indexes PG rows whose supermemory_id is NULL.
var DefaultBackoff = BackoffPolicy{
	MaxAttempts:  3,
	InitialDelay: 250 * time.Millisecond,
	MaxDelay:     1 * time.Second,
	Multiplier:   2.0,
	Jitter:       50 * time.Millisecond,
}

// retryableError signals that the HTTP call failed with a status code the
// backoff policy treats as transient. StatusCode and Body are exposed so
// loggers can distinguish a 429 (quota) from a 503 (availability) without
// re-parsing the error string. RetryAfter carries the server-suggested wait
// from the Retry-After response header when present; the policy honors it.
type retryableError struct {
	StatusCode int
	Body       []byte
	RetryAfter time.Duration
}

// Error formats the retryable failure for logging. Body is truncated to avoid
// flooding logs on large error payloads.
func (e *retryableError) Error() string {
	body := e.Body
	if len(body) > 256 {
		body = body[:256]
	}
	return fmt.Sprintf("supermemory retryable (status %d): %s", e.StatusCode, string(body))
}

// isRetryableStatus returns true for HTTP status codes the policy should retry:
// rate-limit (429), bad-gateway (502), unavailable (503), and gateway-timeout
// (504). Application errors (4xx other than 429) short-circuit immediately.
// 500 is treated as non-retryable because Supermemory uses it for caller-side
// errors (e.g. malformed filter JSON) where retry just wastes the quota.
func isRetryableStatus(code int) bool {
	switch code {
	case 429, 502, 503, 504:
		return true
	}
	return false
}

// MaxRetryAfter bounds how long we'll honor a server-supplied Retry-After
// hint. Without this, a misconfigured server (Retry-After: 3600) could stall
// a pipeline goroutine for an hour inside the backoff sleep. The cap matches
// a single HTTP client timeout so worst-case wait is the same order as a
// fresh request would be.
const MaxRetryAfter = 30 * time.Second

// MaxCumulativeRetryWait bounds the TOTAL time a single retryWithBackoff call
// spends sleeping across all its attempts. MaxRetryAfter caps one sleep, but a
// sequence of honored 30s Retry-After hints could still stall a deadline-less
// write path (pipeline synthesis writes run on an undeadlined ctx) for far
// longer than any read. This ceiling makes a lone call's worst-case wait
// bounded regardless of attempt count or repeated server hints; failed writes
// are recovered by cmd/reconcile-memory anyway, so a capped give-up is cheap.
//
// A var (not const) so tests can shrink it without real multi-minute sleeps.
var MaxCumulativeRetryWait = 2 * time.Minute

// parseRetryAfter reads the Retry-After header per RFC 7231. Supermemory docs
// specify seconds-integer format; we also accept HTTP-date as a fallback.
// Result is capped at MaxRetryAfter so a misconfigured server cannot stall
// the retry loop indefinitely.
//
// strconv.Atoi (not fmt.Sscanf) is used for the integer path because Sscanf
// succeeds on lossy inputs like "0.5" → 0 or "1e2" → 1, under-delaying the
// retry and inviting a retry storm. Atoi rejects anything non-integer.
// Returns 0 on absence or parse failure — caller falls back to exponential.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		// Cap secs BEFORE the multiplication. time.Duration is int64 nanoseconds;
		// `secs * 1e9` overflows for secs > ~9.22e9 and silently wraps negative,
		// returning a past-dated delay that skips the wait entirely.
		const maxRetryAfterSeconds = int(MaxRetryAfter / time.Second)
		if secs > maxRetryAfterSeconds {
			return MaxRetryAfter
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := time.Parse(time.RFC1123, v); err == nil {
		delta := time.Until(t)
		if delta <= 0 {
			return 0
		}
		if delta > MaxRetryAfter {
			return MaxRetryAfter
		}
		return delta
	}
	return 0
}

// retryWithBackoff invokes fn up to policy.MaxAttempts times, sleeping between
// attempts when fn returns a *retryableError. Honors Retry-After from the
// server (takes the larger of exponential delay vs server hint) and stops
// immediately on context cancellation or non-retryable errors.
//
// Goroutine-safety: rand/v2 is safe for concurrent use; the function holds no
// package-level mutable state.
func retryWithBackoff(ctx context.Context, policy BackoffPolicy, fn func(ctx context.Context) error) error {
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}
	delay := policy.InitialDelay
	var lastErr error
	var totalSlept time.Duration
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := fn(ctx); err != nil {
			var retryable *retryableError
			if !errors.As(err, &retryable) {
				return err // non-retryable — short-circuit
			}
			lastErr = err
			if attempt == policy.MaxAttempts {
				break
			}
			// When the server supplies Retry-After, honor it exactly —
			// never subtract jitter from a server-mandated wait, that
			// re-triggers the rate-limit case we're trying to respect.
			// Jitter only modulates our own exponential delay.
			var sleepFor time.Duration
			if retryable.RetryAfter > 0 {
				sleepFor = retryable.RetryAfter
			} else {
				sleepFor = delay
				if policy.Jitter > 0 {
					j := time.Duration(rand.Int64N(int64(policy.Jitter * 2))) // nolint:gosec // non-crypto
					sleepFor += j - policy.Jitter
				}
				if sleepFor < 0 {
					sleepFor = 0
				}
			}
			// Enforce the cumulative wait ceiling: clamp this sleep to the
			// remaining budget, and stop retrying once it's exhausted so a run
			// of honored Retry-After hints can't stall an undeadlined write path
			// indefinitely.
			if remaining := MaxCumulativeRetryWait - totalSlept; sleepFor > remaining {
				sleepFor = remaining
			}
			if sleepFor <= 0 {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepFor):
			}
			totalSlept += sleepFor
			delay = time.Duration(float64(delay) * policy.Multiplier)
			if delay > policy.MaxDelay {
				delay = policy.MaxDelay
			}
			continue
		}
		return nil
	}
	return lastErr
}
