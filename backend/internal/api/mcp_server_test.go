package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BeLazy167/argus/backend/internal/config"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// stubIndexers is the test double for the indexerSource seam.
type stubIndexers struct {
	indexer   memory.Indexer
	available bool
}

func (s *stubIndexers) GetIndexer(context.Context, int64) memory.Indexer { return s.indexer }
func (s *stubIndexers) EmbedderAvailable(context.Context, int64) bool    { return s.available }

func TestRegisterMCPRoutesDisabledIs404(t *testing.T) {
	t.Parallel()
	s := &Server{logger: slog.New(slog.DiscardHandler), cfg: &config.Config{MCPEnabled: false}}
	r := chi.NewRouter()
	s.registerMCPRoutes(r)
	for _, path := range []string{"/mcp", "/.well-known/oauth-protected-resource"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s with MCP disabled: status = %d, want 404", path, rec.Code)
		}
	}
}

func TestRegisterMCPRoutesEnabledServesMetadataAndChallenges(t *testing.T) {
	t.Parallel()
	s := mcpTestServer()
	r := chi.NewRouter()
	s.registerMCPRoutes(r)
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d body=%s", path, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthenticated /mcp: status = %d, WWW-Authenticate = %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}

// Registered once at boot as a schema smoke test: an invalid input/output
// schema panics here, at startup, not on the first production request.
func TestNewMCPServerRegistersAllToolsWithoutPanic(t *testing.T) {
	t.Parallel()
	if mcpTestServer().newMCPServer(tenantScope{}) == nil {
		t.Fatal("newMCPServer returned nil")
	}
}

func TestResolveIndexerNilWhenUnwired(t *testing.T) {
	t.Parallel()
	s := &Server{logger: slog.New(slog.DiscardHandler)}
	if idx := s.resolveIndexer(context.Background(), 1); idx != nil {
		t.Fatalf("unwired server returned an indexer: %v", idx)
	}
	s.indexers = &stubIndexers{}
	if idx := s.resolveIndexer(context.Background(), 1); idx != nil {
		t.Fatalf("stub returning nil indexer should propagate nil, got %v", idx)
	}
}

func TestRequireScope(t *testing.T) {
	t.Parallel()
	tools := &mcpTools{scope: tenantScope{grantedScopes: []string{scopeRead}}}
	if err := tools.requireScope(scopeRead); err != nil {
		t.Fatal(err)
	}
	err := tools.requireScope(scopeMemoryWrite)
	if err == nil || err.Error() != "insufficient scope: argus:memory:write required" {
		t.Fatalf("err = %v", err)
	}
}

// requireMCPInstallationScope: only the org selected during consent, never
// the user's whole installation set, and a hint can only narrow within it.
func TestRequireMCPInstallationScope(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	installA, _ := seedArchitectureRepo(t, ctx, pool)
	installB, _ := seedArchitectureRepo(t, ctx, pool)
	st := store.NewWithDB(pool)
	orgA := "org_mcp_scope_" + strconv.FormatInt(installA, 10)
	if err := st.SetInstallationClerkOrgID(ctx, installA, orgA); err != nil {
		t.Fatal(err)
	}
	const user = "user_mcp_scope"
	// The user belongs to BOTH installations via user_installations; the org
	// claim must still confine the request to A.
	for _, id := range []int64{installA, installB} {
		if _, err := st.LinkUserInstallation(ctx, user, id, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_installations WHERE clerk_user_id = $1`, user)
	})
	s := &Server{store: st, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	var seen []int64
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = getInstallationIDs(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	call := func(org, hint string) int {
		seen = nil
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		ctx := context.WithValue(req.Context(), userIDKey, user)
		if org != "" {
			ctx = context.WithValue(ctx, orgIDKey, org)
		}
		if hint != "" {
			req.Header.Set("X-Installation-ID", hint)
		}
		rec := httptest.NewRecorder()
		s.requireMCPInstallationScope(inner).ServeHTTP(rec, req.WithContext(ctx))
		return rec.Code
	}

	if code := call(orgA, ""); code != http.StatusNoContent || len(seen) != 1 || seen[0] != installA {
		t.Fatalf("selected org: code=%d ids=%v, want [%d]", code, seen, installA)
	}
	if code := call("", ""); code != http.StatusUnauthorized {
		t.Fatalf("missing org claim: code=%d, want 401 (must never fall back to the user's installations)", code)
	}
	if code := call("org_unlinked_"+strconv.FormatInt(installA, 10), ""); code != http.StatusForbidden {
		t.Fatalf("org with no installation: code=%d, want 403", code)
	}
	if code := call(orgA, strconv.FormatInt(installB, 10)); code != http.StatusForbidden {
		t.Fatalf("hint for another installation the user belongs to: code=%d, want 403 (REST silently ignores this; MCP must not)", code)
	}
	if code := call(orgA, "not-a-number"); code != http.StatusForbidden {
		t.Fatalf("malformed hint: code=%d, want 403", code)
	}
	if code := call(orgA, strconv.FormatInt(installA, 10)); code != http.StatusNoContent || len(seen) != 1 || seen[0] != installA {
		t.Fatalf("matching hint: code=%d ids=%v", code, seen)
	}

	// Ordering: the hint is parsed BEFORE installation resolution, because
	// resolution writes — its org branch auto-links the user to the org's
	// installation. A request refused for an unparsable hint must leave no
	// trace, so this uses a user with no user_installations row at all and
	// checks the table directly; the status code alone cannot see the write.
	const fresh = "user_mcp_scope_unlinked"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_installations WHERE clerk_user_id = $1`, fresh)
	})
	freshCall := func(hint string) int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		rctx := context.WithValue(req.Context(), userIDKey, fresh)
		rctx = context.WithValue(rctx, orgIDKey, orgA)
		if hint != "" {
			req.Header.Set("X-Installation-ID", hint)
		}
		rec := httptest.NewRecorder()
		s.requireMCPInstallationScope(inner).ServeHTTP(rec, req.WithContext(rctx))
		return rec.Code
	}
	links := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM user_installations WHERE clerk_user_id = $1`, fresh).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if code := freshCall("not-a-number"); code != http.StatusForbidden {
		t.Fatalf("malformed hint, unlinked user: code=%d, want 403", code)
	}
	if n := links(); n != 0 {
		t.Fatalf("user_installations rows = %d, want 0: a refused request must not auto-link first", n)
	}
	// The control: the same request WITHOUT the malformed hint does resolve
	// and does link, so the count above is about ordering and not about
	// resolution being unreachable for this user.
	if code := freshCall(""); code != http.StatusNoContent {
		t.Fatalf("unlinked user, no hint: code=%d, want 204", code)
	}
	if n := links(); n != 1 {
		t.Fatalf("user_installations rows = %d, want 1 after a resolved request", n)
	}
}
