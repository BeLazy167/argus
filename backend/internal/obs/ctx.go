// Package obs carries telemetry plumbing — typed ctx keys, a PostHog-forwarding
// slog.Handler, an attr allowlist, and a circuit breaker. The package is the
// single place PII/attribution rules live; everything else in the codebase
// emits plain structured slog records.
package obs

import "context"

// ctxKey is package-private so call sites cannot forge or collide with these
// keys — callers must use the typed setters/getters below.
type ctxKey int

const (
	traceIDKey ctxKey = iota
	clerkUserKey
	githubLoginKey
	installationKey
)

// SetTraceID returns a child context carrying the trace id. Empty id is
// still stored (treated equivalent to unset by TraceID).
func SetTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceID reads the trace id from ctx. Returns "" when absent or of wrong type.
func TraceID(ctx context.Context) string {
	v, _ := ctx.Value(traceIDKey).(string)
	return v
}

// SetClerkUser returns a child context carrying the Clerk user id.
func SetClerkUser(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, clerkUserKey, id)
}

// ClerkUser reads the Clerk user id from ctx.
func ClerkUser(ctx context.Context) string {
	v, _ := ctx.Value(clerkUserKey).(string)
	return v
}

// SetGithubLogin returns a child context carrying the GitHub login.
func SetGithubLogin(ctx context.Context, login string) context.Context {
	return context.WithValue(ctx, githubLoginKey, login)
}

// GithubLogin reads the GitHub login from ctx.
func GithubLogin(ctx context.Context) string {
	v, _ := ctx.Value(githubLoginKey).(string)
	return v
}

// SetInstallationID returns a child context carrying the GitHub App
// installation id. Zero is still stored (treated as unset by InstallationID).
func SetInstallationID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, installationKey, id)
}

// InstallationID reads the installation id from ctx. Returns 0 when absent.
func InstallationID(ctx context.Context) int64 {
	v, _ := ctx.Value(installationKey).(int64)
	return v
}
