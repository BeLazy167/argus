package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

// traceIDMiddleware reads X-Argus-Trace-Id from the request (or mints a fresh
// UUID), stashes it on the request ctx via obs.SetTraceID, and echoes it on
// the response header. Installed as the outermost middleware so every downstream
// log/authz/handler sees a trace_id in ctx — including unauthenticated routes.
//
// The response header is the contract that lets the FE's fetch-with-trace
// helper propagate the same trace_id on the next request, stitching together
// a single user gesture across multiple API calls.
func traceIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Argus-Trace-Id")
		// Reject attacker-controlled values: anything not UUID-shaped gets a
		// fresh mint. Prevents injection into slog records / PostHog props /
		// reviews.trace_id DB column, and keeps funnel-breakdown cardinality
		// bounded by real client traffic.
		if _, err := uuid.Parse(id); err != nil {
			id = uuid.NewString()
		}
		w.Header().Set("X-Argus-Trace-Id", id)
		next.ServeHTTP(w, r.WithContext(obs.SetTraceID(r.Context(), id)))
	})
}
