package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryReembedConvergenceRetriesTransientFailureAutomatically(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	converged := make(chan struct{})
	var attempts atomic.Int32
	reembed := func(context.Context) (int, error) {
		switch attempts.Add(1) {
		case 1:
			return 0, errors.New("transient provider failure")
		case 2:
			close(converged)
			return 4, nil
		default:
			return 0, nil
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runMemoryReembedConvergence(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), reembedConvergenceOptions{
			SweepInterval:  time.Hour,
			AttemptTimeout: time.Second,
			RetryMin:       10 * time.Millisecond,
			RetryMax:       20 * time.Millisecond,
		}, reembed)
	}()

	select {
	case <-converged:
	case <-ctx.Done():
		t.Fatalf("transient first failure was not retried: %v", ctx.Err())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("convergence loop did not stop after cancellation")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts=%d, want immediate attempt plus one automatic retry", got)
	}
}

func TestMemoryReembedConvergenceBoundsAttemptsAndStopsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	firstTimedOut := make(chan struct{})
	secondStarted := make(chan struct{})
	var attempts atomic.Int32
	reembed := func(ctx context.Context) (int, error) {
		if attempts.Add(1) == 1 {
			<-ctx.Done()
			close(firstTimedOut)
			return 0, ctx.Err()
		}
		close(secondStarted)
		<-ctx.Done()
		return 0, ctx.Err()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runMemoryReembedConvergence(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), reembedConvergenceOptions{
			SweepInterval:  time.Hour,
			AttemptTimeout: 20 * time.Millisecond,
			RetryMin:       10 * time.Millisecond,
			RetryMax:       20 * time.Millisecond,
		}, reembed)
	}()

	select {
	case <-firstTimedOut:
	case <-time.After(time.Second):
		t.Fatal("fleet attempt was not bounded by its timeout")
	}
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("timed-out attempt was not retried")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("convergence loop did not cleanly stop an in-flight attempt")
	}
}

func TestNextReembedRetryIsBounded(t *testing.T) {
	tests := []struct {
		current time.Duration
		maximum time.Duration
		want    time.Duration
	}{
		{time.Minute, 15 * time.Minute, 2 * time.Minute},
		{8 * time.Minute, 15 * time.Minute, 15 * time.Minute},
		{15 * time.Minute, 15 * time.Minute, 15 * time.Minute},
	}
	for _, tt := range tests {
		if got := nextReembedRetry(tt.current, tt.maximum); got != tt.want {
			t.Errorf("nextReembedRetry(%s, %s)=%s, want %s", tt.current, tt.maximum, got, tt.want)
		}
	}
}
