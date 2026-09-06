package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// The /mcp route is a sibling of /api/v1, so it inherits neither that group's
// 60s timeout nor any body limit, while the SDK handler reads the whole body
// with io.ReadAll and the tool handlers hand the request context straight to
// the embedder. These tests pin both bounds and, importantly, pin that they
// are actually MOUNTED — testing the middleware in isolation would pass even
// if registerMCPRoutes never used it.

func TestMCPLimitRequestBody(t *testing.T) {
	t.Parallel()
	var reached bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	h := mcpLimitRequestBody(inner)

	t.Run("declared oversize is refused before the handler", func(t *testing.T) {
		reached = false
		body := bytes.Repeat([]byte("a"), mcpMaxBodyBytes+1)
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
		if reached {
			t.Fatal("an oversized request must not reach the next handler at all")
		}
	})

	t.Run("undeclared oversize is capped at read time", func(t *testing.T) {
		// ContentLength -1 is the chunked / unknown-length shape, which the
		// declared-size check cannot see. MaxBytesReader is what catches it.
		reached = false
		req := httptest.NewRequest(http.MethodPost, "/mcp", io.NopCloser(strings.NewReader(strings.Repeat("a", mcpMaxBodyBytes+1))))
		req.ContentLength = -1
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if !reached {
			t.Fatal("an undeclared body must reach the handler; the cap applies when it is read")
		}
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413 from the capped read", rec.Code)
		}
	})

	t.Run("a normal body passes", func(t *testing.T) {
		reached = false
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/list","id":1}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent || !reached {
			t.Fatalf("status = %d reached = %v, want 204 and reached", rec.Code, reached)
		}
	})
}

// The limits are mounted on the real route, ahead of authentication. An
// oversized request carries no token here: it must still be refused with 413
// rather than the 401 the auth chain would produce, which is what shows the
// body cap runs first.
func TestMCPRoutesApplyLimitsBeforeAuth(t *testing.T) {
	testJWKS(t) // registerMCPRoutes' auth chain needs the JWKS cache initialized
	s := mcpTestServer()
	r := chi.NewRouter()
	s.registerMCPRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(bytes.Repeat([]byte("a"), mcpMaxBodyBytes+1)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized unauthenticated request: status = %d, want 413 (a 401 means the body cap runs after auth)", rec.Code)
	}

	// A normal unauthenticated request still gets the auth challenge, so the
	// 413 above is attributable to size and not to the route being broken.
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("normal unauthenticated request: status = %d, want 401", rec.Code)
	}
}

// Every handler behind the route's limits runs under a context with a
// deadline. Without it the tool handlers hand an unbounded context to the
// embedder. The probe sits behind mcpRequestLimits — the same slice
// registerMCPRoutes applies — so this cannot pass against a stale copy.
func TestMCPRequestLimitsGiveTheContextADeadline(t *testing.T) {
	t.Parallel()
	var (
		hasDeadline bool
		remaining   time.Duration
	)
	probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var d time.Time
		d, hasDeadline = r.Context().Deadline()
		if hasDeadline {
			remaining = time.Until(d)
		}
	})
	r := chi.NewRouter()
	r.Route("/mcp", func(r chi.Router) {
		r.Use(mcpRequestLimits()...)
		r.Handle("/", probe)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/mcp/", strings.NewReader(`{}`)))
	if !hasDeadline {
		t.Fatal("request context has no deadline: the timeout middleware is not applied")
	}
	if remaining <= 0 || remaining > mcpRequestTimeout {
		t.Fatalf("deadline in %s, want within (0, %s]", remaining, mcpRequestTimeout)
	}
}
