// Package api — handlers_pprof.go: admin-gated pprof endpoints.
//
// Exposes the stdlib net/http/pprof handlers under /debug/pprof/*, gated by a
// shared-secret header X-Admin-Token matching env ADMIN_DEBUG_TOKEN. When the
// env var is unset, all /debug/pprof/* paths return 404 (not 403) so we don't
// advertise the surface. An unauthenticated heap-dump endpoint would be a
// data-exfiltration vector.
package api

import (
	"crypto/subtle"
	"net/http"
	"net/http/pprof"
	"os"

	"github.com/go-chi/chi/v5"
)

// registerPprofRoutes wires /debug/pprof/* onto r. If ADMIN_DEBUG_TOKEN is not
// set, the routes are simply not registered — any request 404s via the chi
// default handler, matching "don't advertise" semantics.
func registerPprofRoutes(r chi.Router) {
	token := os.Getenv("ADMIN_DEBUG_TOKEN")
	if token == "" {
		return
	}
	r.Route("/debug/pprof", func(r chi.Router) {
		r.Use(requireAdminToken(token))
		r.HandleFunc("/", pprof.Index)
		r.HandleFunc("/cmdline", pprof.Cmdline)
		r.HandleFunc("/profile", pprof.Profile)
		r.HandleFunc("/symbol", pprof.Symbol)
		r.HandleFunc("/trace", pprof.Trace)
		// Named profile handlers — chi's URLParam gives us the name.
		r.Handle("/allocs", pprof.Handler("allocs"))
		r.Handle("/block", pprof.Handler("block"))
		r.Handle("/goroutine", pprof.Handler("goroutine"))
		r.Handle("/heap", pprof.Handler("heap"))
		r.Handle("/mutex", pprof.Handler("mutex"))
		r.Handle("/threadcreate", pprof.Handler("threadcreate"))
	})
}

// requireAdminToken is constant-time comparison against the expected token.
// Anything missing or wrong → 404 (to not advertise the surface).
func requireAdminToken(expected string) func(http.Handler) http.Handler {
	want := []byte(expected)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := []byte(r.Header.Get("X-Admin-Token"))
			if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
