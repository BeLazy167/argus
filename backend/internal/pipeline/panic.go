// Package pipeline — panic.go centralises the "a background goroutine
// exploded" telemetry path. The pattern recurs ~20 times across orchestrator
// and crosspr_stage; keeping the emit logic in one place lets the slog attr
// set stay consistent and keeps the PostHog allowlist honest (every new key
// here is reviewed once, not per site).
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
)

// panicRedactor strips token-shaped strings and absolute file paths from
// a recovered panic message before it leaves the process.
//
//   - `/…/argus/backend/…` paths would expose the build tree; we collapse
//     them to the last path segment.
//   - Long base64/hex runs are treated as secrets (API keys, JWT fragments).
//
// The regexes are intentionally conservative — we'd rather keep stack-like
// detail than accidentally over-scrub a useful `nil pointer deref` message.
var (
	panicPathRegex   = regexp.MustCompile(`(?:/[a-zA-Z0-9_.\-+]+){2,}`)
	panicSecretRegex = regexp.MustCompile(`[A-Za-z0-9_-]{32,}`)
)

// redactPanic returns a cleaned-up version of r suitable for sending to
// PostHog. It collapses absolute paths to their basename and masks
// token-shaped substrings — NEVER emits raw stack frames (those go to
// stdout via the caller's slog.Error).
func redactPanic(r any) string {
	raw := fmt.Sprint(r)
	raw = panicPathRegex.ReplaceAllStringFunc(raw, func(match string) string {
		// Keep just the last segment so the message still names the file.
		for i := len(match) - 1; i >= 0; i-- {
			if match[i] == '/' {
				return match[i+1:]
			}
		}
		return match
	})
	raw = panicSecretRegex.ReplaceAllString(raw, "<redacted>")
	return raw
}

// emitPipelinePanicEvent fires the pipeline.panic_recovered slog event so
// PostHog dashboards see every background-goroutine explosion. Callers are
// expected to ALSO have logged their own Error with the full stack trace
// and a descriptive msg; this helper is purely for the PostHog funnel.
// Safe to call with an empty stage (unknown context).
func emitPipelinePanicEvent(ctx context.Context, logger *slog.Logger, stage string, r any, traceID string) {
	if logger == nil {
		return
	}
	logger.ErrorContext(ctx, "pipeline panic recovered",
		slog.String("event", "pipeline.panic_recovered"),
		slog.String("stage", stage),
		slog.String("panic_msg_redacted", redactPanic(r)),
		slog.String("trace_id", traceID),
	)
}

// EmitPipelinePanicEvent is the exported alias for cross-package callers
// (e.g. internal/api webhook goroutines) that need the same PostHog funnel
// event when they recover a panic. Identical semantics to the unexported
// form; kept separate so the intra-pipeline spelling stays lowercase.
func EmitPipelinePanicEvent(ctx context.Context, logger *slog.Logger, stage string, r any, traceID string) {
	emitPipelinePanicEvent(ctx, logger, stage, r, traceID)
}

