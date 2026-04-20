package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

// TestTraceIDMiddleware pins three invariants:
//   1. Supplied X-Argus-Trace-Id flows through to ctx and back out on response.
//   2. Missing header mints a fresh (non-empty) UUID.
//   3. The echoed response header matches the ctx value exactly — this is the
//      contract FE fetch-with-trace.ts depends on for propagation.
func TestTraceIDMiddleware(t *testing.T) {
	t.Run("echoes supplied trace id into ctx and response", func(t *testing.T) {
		const want = "11111111-2222-3333-4444-555555555555"
		var got string
		h := traceIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = obs.TraceID(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("X-Argus-Trace-Id", want)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if got != want {
			t.Errorf("ctx trace id = %q, want %q", got, want)
		}
		if echoed := rec.Header().Get("X-Argus-Trace-Id"); echoed != want {
			t.Errorf("response header = %q, want %q", echoed, want)
		}
	})

	t.Run("mints uuid when header absent", func(t *testing.T) {
		var ctxTrace string
		h := traceIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctxTrace = obs.TraceID(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if ctxTrace == "" {
			t.Fatal("trace id missing from ctx")
		}
		if echoed := rec.Header().Get("X-Argus-Trace-Id"); echoed != ctxTrace {
			t.Errorf("header %q != ctx %q — propagation contract broken", echoed, ctxTrace)
		}
		// Rough UUID v4 check: 36 chars, hyphens at the right spots. We don't
		// rewrite a UUID validator — just guard the length so a regression that
		// emits e.g. a Unix timestamp is caught.
		if len(ctxTrace) != 36 {
			t.Errorf("minted id has length %d, want 36 (UUID v4)", len(ctxTrace))
		}
	})
}
