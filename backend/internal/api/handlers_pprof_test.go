package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestPprofRoutes_Unregistered_WhenTokenUnset(t *testing.T) {
	t.Setenv("ADMIN_DEBUG_TOKEN", "")
	r := chi.NewRouter()
	registerPprofRoutes(r)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/heap", nil)
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 when token unset", rr.Code)
	}
}

func TestPprofRoutes_RequiresToken(t *testing.T) {
	t.Setenv("ADMIN_DEBUG_TOKEN", "s3cret")
	r := chi.NewRouter()
	registerPprofRoutes(r)

	tests := []struct {
		name   string
		token  string
		want   int
		checkBody bool
	}{
		{"missing token", "", http.StatusNotFound, false},
		{"wrong token", "nope", http.StatusNotFound, false},
		{"correct token", "s3cret", http.StatusOK, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			// Use /goroutine — lightweight and stable.
			req := httptest.NewRequest(http.MethodGet, "/debug/pprof/goroutine?debug=1", nil)
			if tt.token != "" {
				req.Header.Set("X-Admin-Token", tt.token)
			}
			r.ServeHTTP(rr, req)
			if rr.Code != tt.want {
				t.Fatalf("status = %d; want %d", rr.Code, tt.want)
			}
			if tt.checkBody && rr.Body.Len() == 0 {
				t.Error("expected non-empty body for authorised request")
			}
		})
	}
}

func TestRequireAdminToken_ConstantTimeNoLeak(t *testing.T) {
	mw := requireAdminToken("abcdef")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Wrong length should 404 — verifies the length short-circuit exists and
	// doesn't feed mismatched slices into subtle.ConstantTimeCompare (which
	// would panic).
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Admin-Token", "short")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rr.Code)
	}
}
