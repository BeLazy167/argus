package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

// pipelineRecoverySweepInterval balances how long a crashed run stays stranded
// against DB scan load. Claims still require updated_at older than the state
// machine's staleness floor, so a shorter interval cannot steal live runs —
// their rows are heartbeated while stages execute.
const pipelineRecoverySweepInterval = 5 * time.Minute

// pipelineRecoveryFirstSweepDelay holds the first PERIODIC sweep back until a
// rolling deploy has finished. The boot sweep still runs immediately (that is
// the pre-existing exposure, unchanged), but during a deploy the older binary
// on the other machine refreshes updated_at only at stage boundaries, so its
// live long stages look stale to this one. Ticking every 5 minutes into that
// window would repeatedly offer up a live run to be claimed; waiting past the
// staleness floor lets the fleet converge on binaries that all heartbeat.
const pipelineRecoveryFirstSweepDelay = 15 * time.Minute

// runPipelineRecoverySweeper scans for stranded pipeline runs immediately,
// then periodically for the process lifetime. A boot-time one-shot cannot
// rescue a run that crashed moments before this process started: at boot such
// a row is younger than the staleness floor, and it only becomes claimable
// later, when nothing used to look again (observed in production 2026-08-29:
// an OOM kill mid-posting restarted the machine, the boot scan found nothing,
// and the review sat on "posting" until a human intervened).
func runPipelineRecoverySweeper(
	ctx context.Context,
	logger *slog.Logger,
	firstDelay time.Duration,
	interval time.Duration,
	sweep func(context.Context) error,
) {
	workerStarted := time.Now()
	logger.InfoContext(ctx, "pipeline recovery sweeper started", "sweep_interval", interval, "first_sweep_delay", firstDelay)
	// The worker-wide recover the boot-time goroutine had: sweepOnce arms its
	// own only around the sweep call, so a panic in this loop's logging (a
	// slog handler failing to allocate under the very memory pressure this
	// worker exists for) would otherwise take down the whole process and every
	// in-flight review with it.
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorContext(context.WithoutCancel(ctx), "pipeline recovery sweeper panic",
				"duration_ms", time.Since(workerStarted).Milliseconds(), "recover", r)
			return
		}
		logger.InfoContext(context.WithoutCancel(ctx), "pipeline recovery sweeper stopped",
			"duration_ms", time.Since(workerStarted).Milliseconds(), "reason", ctx.Err())
	}()

	delay := firstDelay
	for {
		sweepOnce(ctx, logger, sweep)
		timer := time.NewTimer(delay)
		delay = interval
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

// sweepOnce isolates one scan so a panic in a claimed run's resume path ends
// that scan, not recovery for every later crash in the process lifetime.
func sweepOnce(ctx context.Context, logger *slog.Logger, sweep func(context.Context) error) {
	operationID := obs.NewLogID()
	started := time.Now()
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorContext(ctx, "pipeline recovery sweep panic",
				"operation_id", operationID, "duration_ms", time.Since(started).Milliseconds(), "recover", r)
		}
	}()
	err := sweep(ctx)
	switch {
	case err == nil:
	case ctx.Err() != nil:
		// Shutdown can race a timer fire; don't page on a scan that died of
		// process shutdown, but don't swallow what it saw either.
		logger.InfoContext(context.WithoutCancel(ctx), "pipeline recovery sweep ended during shutdown",
			"operation_id", operationID, "duration_ms", time.Since(started).Milliseconds(), "error", err)
	default:
		logger.ErrorContext(ctx, "pipeline recovery sweep failed",
			"operation_id", operationID, "duration_ms", time.Since(started).Milliseconds(), "error", err)
	}
}
