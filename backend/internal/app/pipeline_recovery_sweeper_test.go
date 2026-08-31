package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRunPipelineRecoverySweeper_SweepsPeriodically proves the sweeper is a
// loop, not a boot-time one-shot: a run that crashes moments before boot is
// younger than the staleness floor at the first scan and is only claimable
// on a later one.
func TestRunPipelineRecoverySweeper_SweepsPeriodically(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sweeps := 0
	done := make(chan struct{})
	sweep := func(context.Context) error {
		sweeps++
		if sweeps == 3 {
			cancel()
		}
		return nil
	}

	go func() {
		runPipelineRecoverySweeper(ctx, discardLogger(), time.Millisecond, time.Millisecond, sweep)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sweeper did not stop after context cancellation")
	}
	if sweeps < 3 {
		t.Fatalf("sweeps = %d, want at least 3 (must re-scan periodically)", sweeps)
	}
}

// TestRunPipelineRecoverySweeper_SurvivesErrors proves a failed scan is logged
// and retried on the next interval instead of ending recovery for the process.
func TestRunPipelineRecoverySweeper_SurvivesErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sweeps := 0
	done := make(chan struct{})
	sweep := func(context.Context) error {
		sweeps++
		if sweeps == 2 {
			cancel()
			return nil
		}
		return errors.New("transient scan failure")
	}

	go func() {
		runPipelineRecoverySweeper(ctx, discardLogger(), time.Millisecond, time.Millisecond, sweep)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sweeper stopped on a scan error instead of retrying")
	}
	// >= 2, not == 2: cancellation can race the 1ms timer, letting one extra
	// sweep through before the loop observes ctx.Done().
	if sweeps < 2 {
		t.Fatalf("sweeps = %d, want at least 2 (error then retry)", sweeps)
	}
}

// TestRunPipelineRecoverySweeper_SurvivesPanics proves a panicking scan does
// not kill the loop (a panic in one claimed run's resume path must not end
// recovery for every later crash).
func TestRunPipelineRecoverySweeper_SurvivesPanics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sweeps := 0
	done := make(chan struct{})
	sweep := func(context.Context) error {
		sweeps++
		if sweeps == 2 {
			cancel()
			return nil
		}
		panic("scan panic")
	}

	go func() {
		runPipelineRecoverySweeper(ctx, discardLogger(), time.Millisecond, time.Millisecond, sweep)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sweeper died on a panicking scan instead of retrying")
	}
	if sweeps < 2 {
		t.Fatalf("sweeps = %d, want at least 2 (panic then retry)", sweeps)
	}
}

// TestRunPipelineRecoverySweeper_HoldsFirstPeriodicSweep pins the rolling-deploy
// window: the boot sweep runs immediately, but the first PERIODIC sweep waits
// out firstDelay. During a deploy the older binary refreshes updated_at only at
// stage boundaries, so its live long stages look stale to this one; ticking at
// the steady interval into that window repeatedly offers a live run up to be
// claimed, which is the duplicate-review incident.
func TestRunPipelineRecoverySweeper_HoldsFirstPeriodicSweep(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const firstDelay = 300 * time.Millisecond
	var mu sync.Mutex
	var sweepAt []time.Duration
	started := time.Now()
	sweep := func(context.Context) error {
		mu.Lock()
		sweepAt = append(sweepAt, time.Since(started))
		n := len(sweepAt)
		mu.Unlock()
		if n >= 2 {
			cancel()
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		runPipelineRecoverySweeper(ctx, discardLogger(), firstDelay, time.Millisecond, sweep)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("sweeper did not stop")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sweepAt) < 2 {
		t.Fatalf("sweeps = %d, want at least 2", len(sweepAt))
	}
	if sweepAt[0] > firstDelay/2 {
		t.Errorf("boot sweep ran at %v, want immediately", sweepAt[0])
	}
	// The second sweep must wait out firstDelay, not the 1ms steady interval.
	if sweepAt[1] < firstDelay {
		t.Errorf("second sweep ran at %v, want >= firstDelay %v — the rolling-deploy hold was skipped", sweepAt[1], firstDelay)
	}
}
