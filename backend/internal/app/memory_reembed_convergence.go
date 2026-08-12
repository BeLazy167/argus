package app

import (
	"context"
	"log/slog"
	"time"
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
	retryDelay := options.RetryMin

	for {
		attemptCtx, cancel := context.WithTimeout(ctx, options.AttemptTimeout)
		repaired, err := reembed(attemptCtx)
		cancel()
		if ctx.Err() != nil {
			return
		}

		delay := options.SweepInterval
		if err != nil {
			logger.Warn("memory reembed convergence", "repaired", repaired, "retry_in", retryDelay, "error", err)
			delay = retryDelay
			retryDelay = nextReembedRetry(retryDelay, options.RetryMax)
		} else {
			logger.Info("memory reembed convergence complete", "repaired", repaired, "next_sweep_in", options.SweepInterval)
			retryDelay = options.RetryMin
		}

		timer := time.NewTimer(delay)
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
