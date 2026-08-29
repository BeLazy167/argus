package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

const (
	memoryReembedSweepInterval  = 6 * time.Hour
	memoryReembedAttemptTimeout = 2 * time.Hour
	memoryReembedRetryMin       = time.Minute
	memoryReembedRetryMax       = 15 * time.Minute
)

type reembedConvergenceOptions struct {
	SweepInterval  time.Duration
	AttemptTimeout time.Duration
	RetryMin       time.Duration
	RetryMax       time.Duration
}

func defaultReembedConvergenceOptions() reembedConvergenceOptions {
	return reembedConvergenceOptions{
		SweepInterval:  memoryReembedSweepInterval,
		AttemptTimeout: memoryReembedAttemptTimeout,
		RetryMin:       memoryReembedRetryMin,
		RetryMax:       memoryReembedRetryMax,
	}
}

// runMemoryReembedConvergence immediately checks the fleet, then repeats a
// healthy sweep periodically. Failed sweeps retry with bounded exponential
// backoff. ReembedAllCurrentSpaces supplies the cross-replica tenant advisory
// locks and per-process capacity bound; this loop supplies durable process
// lifetime retries without another database work table.
func runMemoryReembedConvergence(
	ctx context.Context,
	logger *slog.Logger,
	options reembedConvergenceOptions,
	reembed func(context.Context) (int, error),
) {
	if logger == nil {
		logger = slog.Default()
	}
	if reembed == nil {
		logger.Error("memory reembed convergence is not configured")
		return
	}
	options = normalizedReembedConvergenceOptions(options)
	workerStarted := time.Now()
	logger.InfoContext(ctx, "memory reembed convergence worker started",
		"sweep_interval", options.SweepInterval, "attempt_timeout", options.AttemptTimeout,
		"retry_min", options.RetryMin, "retry_max", options.RetryMax)
	defer func() {
		logger.InfoContext(context.WithoutCancel(ctx), "memory reembed convergence worker stopped",
			"duration_ms", time.Since(workerStarted).Milliseconds(), "reason", ctx.Err())
	}()
	retryDelay := options.RetryMin
	attemptNumber := 0

	for {
		attemptNumber++
		operationID := obs.NewLogID()
		attemptStarted := time.Now()
		logger.InfoContext(ctx, "memory reembed convergence attempt started",
			"operation_id", operationID, "attempt", attemptNumber, "timeout", options.AttemptTimeout)
		attemptCtx, cancel := context.WithTimeout(ctx, options.AttemptTimeout)
		repaired, err := reembed(attemptCtx)
		attemptErr := attemptCtx.Err()
		cancel()
		if ctx.Err() != nil {
			logger.InfoContext(context.WithoutCancel(ctx), "memory reembed convergence attempt stopped",
				"operation_id", operationID, "attempt", attemptNumber, "repaired", repaired,
				"duration_ms", time.Since(attemptStarted).Milliseconds(), "reason", ctx.Err(), "error", err)
			return
		}

		delay := options.SweepInterval
		if err != nil {
			logger.WarnContext(ctx, "memory reembed convergence attempt failed",
				"operation_id", operationID, "attempt", attemptNumber, "repaired", repaired,
				"timed_out", attemptErr == context.DeadlineExceeded,
				"duration_ms", time.Since(attemptStarted).Milliseconds(), "retry_in", retryDelay, "error", err)
			delay = retryDelay
			retryDelay = nextReembedRetry(retryDelay, options.RetryMax)
		} else {
			logger.InfoContext(ctx, "memory reembed convergence attempt completed",
				"operation_id", operationID, "attempt", attemptNumber, "repaired", repaired,
				"duration_ms", time.Since(attemptStarted).Milliseconds(), "next_sweep_in", options.SweepInterval)
			retryDelay = options.RetryMin
		}

		logger.InfoContext(ctx, "memory reembed convergence wait started",
			"operation_id", operationID, "delay", delay, "next_attempt", attemptNumber+1)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			logger.InfoContext(context.WithoutCancel(ctx), "memory reembed convergence wait stopped",
				"operation_id", operationID, "reason", ctx.Err())
			return
		case <-timer.C:
			logger.InfoContext(ctx, "memory reembed convergence wait completed",
				"operation_id", operationID, "next_attempt", attemptNumber+1)
		}
	}
}

func normalizedReembedConvergenceOptions(options reembedConvergenceOptions) reembedConvergenceOptions {
	defaults := defaultReembedConvergenceOptions()
	if options.SweepInterval <= 0 {
		options.SweepInterval = defaults.SweepInterval
	}
	if options.AttemptTimeout <= 0 {
		options.AttemptTimeout = defaults.AttemptTimeout
	}
	if options.RetryMin <= 0 {
		options.RetryMin = defaults.RetryMin
	}
	if options.RetryMax < options.RetryMin {
		options.RetryMax = options.RetryMin
	}
	return options
}

func nextReembedRetry(current, maximum time.Duration) time.Duration {
	if current >= maximum || current > maximum/2 {
		return maximum
	}
	return current * 2
}
