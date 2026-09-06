package api

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeLazy167/argus/backend/internal/config"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/store"
)

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

type e2eFixture struct {
	srv                *httptest.Server
	key                *rsa.PrivateKey
	kid                string
	installA, installB int64
	repoA, repoB       int64
	orgA, orgB         string
}

// newE2EFixture mounts the real route on an httptest server with a JWKS the
// test controls, one user who belongs to two orgs/installations, and a token
// minter. Every test below goes through the full chain.
func newE2EFixture(t *testing.T) *e2eFixture {
	t.Helper()
	pool, ctx := architectureTestPool(t)
	installA, repoA := seedArchitectureRepo(t, ctx, pool)
	installB, repoB := seedArchitectureRepo(t, ctx, pool)
	st := store.NewWithDB(pool)
	orgA := "org_e2e_a_" + strconv.FormatInt(installA, 10)
	orgB := "org_e2e_b_" + strconv.FormatInt(installB, 10)
	for _, pair := range []struct {
		id  int64
		org string
	}{{installA, orgA}, {installB, orgB}} {
		if err := st.SetInstallationClerkOrgID(ctx, pair.id, pair.org); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []int64{installA, installB} {
		if _, err := st.LinkUserInstallation(ctx, e2eUser, id, "org:member"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_installations WHERE clerk_user_id = $1`, e2eUser)
	})

	key, kid := testJWKS(t)
	s := &Server{
		store:  st,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		cfg:    &config.Config{MCPEnabled: true, ClerkIssuerURL: testIssuer, MCPResourceURL: testResource},
	}
	r := chi.NewRouter()
	s.registerMCPRoutes(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &e2eFixture{srv: srv, key: key, kid: kid, installA: installA, installB: installB, repoA: repoA, repoB: repoB, orgA: orgA, orgB: orgB}
}

const e2eUser = "user_mcp_e2e"

// mint signs an access token for e2eUser in the given org with the given
// space-delimited scopes.
func (f *e2eFixture) mint(t *testing.T, org, scopes string) string {
	t.Helper()
	claims := accessTokenClaims()
	claims["sub"] = e2eUser
	claims["org_id"] = org
	claims["scope"] = scopes
	return signTestJWT(t, f.key, f.kid, claims)
}

func (f *e2eFixture) session(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   f.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func decodeTool[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	if len(res.Content) == 0 {
		t.Fatal("no content")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T", res.Content[0])
	}
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, text.Text)
	}
	return out
}

const allScopes = "argus:read argus:memory:write user:org:read"

func TestMCPEndToEndSelectedOrgAndScopes(t *testing.T) {
	f := newE2EFixture(t)
	ctx := context.Background()

	// Authorized for org A: the whole tool set is advertised; list_repos shows
	// only A even though the user also belongs to B.
	sess := f.session(t, f.mint(t, f.orgA, allScopes))
	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"list_repos": false, "search_memory": false, "get_memory_briefing": false, "create_memory": false, "delete_memory": false, "retire_memory": false, "list_reviews": false, "get_review_status": false, "get_review": false}
	for _, tool := range tools.Tools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %s not advertised", name)
		}
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_repos", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("list_repos: err=%v res=%+v", err, res)
	}
	out := decodeTool[listReposOutput](t, res)
	var sawA, sawB bool
	for _, repo := range out.Repos {
		sawA = sawA || repo.RepoID == f.repoA
		sawB = sawB || repo.RepoID == f.repoB
	}
	if !sawA || sawB {
		t.Fatalf("org A token saw A=%v B=%v; the user belongs to both installations but authorized only A", sawA, sawB)
	}
	// A tool call naming B's repo is not accessible through A's connection.
	res, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_reviews", Arguments: map[string]any{"repo_id": f.repoB}})
	if err != nil || !res.IsError {
		t.Fatalf("B's repo through A's token must be a tool error: err=%v res=%+v", err, res)
	}

	// Reauthorizing for B permits B only.
	sessB := f.session(t, f.mint(t, f.orgB, allScopes))
	res, err = sessB.CallTool(ctx, &mcp.CallToolParams{Name: "list_repos", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatal(err)
	}
	for _, repo := range decodeTool[listReposOutput](t, res).Repos {
		if repo.RepoID == f.repoA {
			t.Fatal("org B token saw A's repo")
		}
	}

	// A read-only grant cannot write, even with every acknowledgment flag set.
	ro := f.session(t, f.mint(t, f.orgA, "argus:read user:org:read"))
	res, err = ro.CallTool(ctx, &mcp.CallToolParams{Name: "create_memory", Arguments: map[string]any{
		"installation_id": f.installA, "repo_id": f.repoA, "content": "should not land", "confirm_duplicate": true, "confirm_shared": true,
	}})
	if err != nil || !res.IsError {
		t.Fatalf("read-only write must be a tool error: err=%v res=%+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "insufficient scope") {
		t.Fatalf("scope denial must use the fixed error, got %q", text)
	}

	// Raw HTTP checks of the auth chain on the mounted route.
	post := func(token, hint string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if hint != "" {
			req.Header.Set("X-Installation-ID", hint)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}
	if resp := post("", ""); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("no token: status=%d WWW-Authenticate=%q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if resp := post(f.mint(t, f.orgA, allScopes), strconv.FormatInt(f.installB, 10)); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("hint for B on A's token: status=%d, want 403", resp.StatusCode)
	}
}

// Every tool that takes an id must answer errNotAccessible for a foreign one.
// Runs each handler directly so a regression in any one tool is named.
func TestMCPTenantIsolationSweep(t *testing.T) {
	pool, ctx := architectureTestPool(t)
	installA, _ := seedArchitectureRepo(t, ctx, pool)
	installB, repoB := seedArchitectureRepo(t, ctx, pool)
	reviewB := seedReview(t, ctx, pool, repoB, 1, "completed")
	st := store.NewWithDB(pool)
	src := "manual"
	cidB := "sm_sweep_b"
	patternB, err := st.CreatePattern(ctx, installB, &repoB, "theirs", nil, nil, &src, nil, nil, &cidB, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id = $1`, installB)
		_, _ = pool.Exec(bg, `DELETE FROM patterns WHERE installation_id = $1`, installB)
	})
	idx := &recordingIndexer{retireFn: func(memory.RetireRequest) (memory.RetireResult, error) {
		t.Fatal("retire must be rejected before reaching the indexer")
		return memory.RetireResult{}, nil
	}}
	s := &Server{store: st, logger: slog.New(slog.DiscardHandler), indexers: &stubIndexers{indexer: idx, available: true}}
	tools := &mcpTools{srv: s, scope: writeScope(installA)}

	cases := []struct {
		name string
		call func() error
	}{
		{"search_memory repo", func() error {
			_, _, e := tools.searchMemory(ctx, nil, searchMemoryInput{RepoID: repoB, Query: "q"})
			return e
		}},
		{"search_memory shared", func() error {
			_, _, e := tools.searchMemory(ctx, nil, searchMemoryInput{InstallationID: installB, Scope: "shared", Query: "q"})
			return e
		}},
		{"get_memory_briefing", func() error {
			_, _, e := tools.getMemoryBriefing(ctx, nil, getMemoryBriefingInput{RepoID: repoB, Query: "q"})
			return e
		}},
		{"create_memory installation", func() error {
			_, _, e := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installB, RepoID: &repoB, Content: "x", ConfirmDuplicate: true})
			return e
		}},
		{"create_memory repo", func() error {
			_, _, e := tools.createMemory(ctx, nil, createMemoryInput{InstallationID: installA, RepoID: &repoB, Content: "x", ConfirmDuplicate: true})
			return e
		}},
		{"delete_memory", func() error {
			_, _, e := tools.deleteMemory(ctx, nil, deleteMemoryInput{PatternID: patternB.ID, ConfirmPipelineLearned: true})
			return e
		}},
		{"retire_memory", func() error {
			_, _, e := tools.retireMemory(ctx, nil, retireMemoryInput{InstallationID: installB, CustomID: cidB, Reason: "r", ConfirmPipelineLearned: true})
			return e
		}},
		{"list_reviews", func() error { _, _, e := tools.listReviews(ctx, nil, listReviewsInput{RepoID: repoB}); return e }},
		{"get_review_status", func() error {
			_, _, e := tools.getReviewStatus(ctx, nil, getReviewStatusInput{ReviewID: reviewB.String()})
			return e
		}},
		{"get_review", func() error {
			_, _, e := tools.getReview(ctx, nil, getReviewInput{ReviewID: reviewB.String()})
			return e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil || err.Error() != errNotAccessible.Error() {
				t.Fatalf("%s: err = %v, want %q", tc.name, err, errNotAccessible)
			}
		})
	}
	if _, err := st.GetPattern(ctx, patternB.ID); err != nil {
		t.Fatalf("B's pattern must be untouched by the sweep: %v", err)
	}
	// list_repos has no id argument; its isolation is TestListReposIsScopedToCaller.
}

// rawMCP posts one JSON-RPC message to the mounted /mcp route with an explicit
// bearer token and, optionally, an Mcp-Session-Id header. The SDK client always
// sends the session id it was given, so only a hand-built request can pair one
// tenant's session id with another tenant's token.
func (f *e2eFixture) rawMCP(t *testing.T, token, sessionID, body string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

// decodeRawTool pulls the tool payload out of a raw JSON-RPC response envelope.
func decodeRawTool[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var envelope struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("envelope: %v\n%s", err, raw)
	}
	if envelope.Error != nil || envelope.Result.IsError || len(envelope.Result.Content) == 0 {
		t.Fatalf("tool call did not succeed: %s", raw)
	}
	var out T
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &out); err != nil {
		t.Fatalf("payload: %v\n%s", err, envelope.Result.Content[0].Text)
	}
	return out
}

// TestMCPSessionIDCarriesNoTenantState pins StreamableHTTPOptions.Stateless.
//
// The tenant is frozen into a closure by mcpGetServer, which runs once per
// JSON-RPC request only because the handler is stateless. Flip Stateless to
// false and the handler instead keeps the *mcp.Server built at initialize in a
// map keyed by session id, so a later request carrying that id is answered by
// the session that created it — the caller's own token is verified and then
// ignored. Both orgs here belong to the same user, so the SDK's own
// session-hijacking guard (which only compares user ids) does not catch it.
//
// Nothing else in the suite fails when Stateless is flipped, which is why this
// test exists.
func TestMCPSessionIDCarriesNoTenantState(t *testing.T) {
	f := newE2EFixture(t)
	const listRepos = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_repos","arguments":{}}}`

	// A real handshake as org A. The server hands out a session id even in
	// stateless mode (it is a fresh throwaway per request), so this is the id a
	// client would send back.
	sessA := f.session(t, f.mint(t, f.orgA, allScopes))
	idA := sessA.ID()
	if idA == "" {
		t.Fatal("no session id to replay; the leak this test guards needs one")
	}

	// A's session id, B's token. The answer must come from the token.
	resp, raw := f.rawMCP(t, f.mint(t, f.orgB, allScopes), idA, listRepos)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	out := decodeRawTool[listReposOutput](t, raw)
	var sawA, sawB bool
	for _, repo := range out.Repos {
		sawA = sawA || repo.RepoID == f.repoA
		sawB = sawB || repo.RepoID == f.repoB
	}
	if sawA || !sawB {
		t.Fatalf("org A's session id replayed with org B's token returned A=%v B=%v; the session must carry no tenant", sawA, sawB)
	}

	// The complementary half: a session id the server never issued is not an
	// error either, because no session is ever stored under one. A stateful
	// handler answers this with 404 "session not found".
	resp, raw = f.rawMCP(t, f.mint(t, f.orgB, allScopes), "MCP-SESSION-THAT-NEVER-EXISTED", listRepos)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown session id: status = %d, body = %s", resp.StatusCode, raw)
	}
	for _, repo := range decodeRawTool[listReposOutput](t, raw).Repos {
		if repo.RepoID == f.repoA {
			t.Fatal("org B's token saw A's repo under an unknown session id")
		}
	}
}
