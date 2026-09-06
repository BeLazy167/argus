package api

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/BeLazy167/argus/backend/internal/config"
)

// testJWKS serves one RSA key and points the package-level JWKS cache at it.
// The cache is package state, so tests using this must not run in parallel
// with other JWKS-dependent tests.
func testJWKS(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "test-kid"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": kid, "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(srv.Close)
	InitJWKS(srv.URL, slog.New(slog.DiscardHandler))
	t.Cleanup(func() { cache = nil })
	return key, kid
}

func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signing := enc(map[string]string{"alg": "RS256", "kid": kid}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

const (
	testIssuer   = "https://clerk.example"
	testResource = "https://api.example/mcp"
)

// accessTokenClaims is a valid MCP access token; tests mutate one field.
func accessTokenClaims() map[string]any {
	now := time.Now()
	return map[string]any{
		"sub": "user_1", "iss": testIssuer, "aud": testResource,
		"exp": float64(now.Add(time.Hour).Unix()), "nbf": float64(now.Add(-time.Minute).Unix()),
		"org_id": "org_1", "org_role": "org:member",
		"scope": "argus:read argus:memory:write user:org:read",
	}
}

func mcpTestServer() *Server {
	return &Server{logger: slog.New(slog.DiscardHandler), cfg: &config.Config{
		MCPEnabled: true, ClerkIssuerURL: testIssuer, MCPResourceURL: testResource,
	}}
}

func TestVerifyJWTDecodesMCPClaims(t *testing.T) {
	key, kid := testJWKS(t)
	c, err := verifyJWT(signTestJWT(t, key, kid, accessTokenClaims()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != "user_1" || c.Iss != testIssuer || len(c.Aud) != 1 || c.Aud[0] != testResource || c.OrgID != "org_1" || c.Nbf == 0 || c.Scope == "" {
		t.Fatalf("claims = %+v", c)
	}
	arr := accessTokenClaims()
	arr["aud"] = []string{"a", testResource}
	c, err = verifyJWT(signTestJWT(t, key, kid, arr))
	if err != nil || len(c.Aud) != 2 {
		t.Fatalf("array aud: %+v %v", c, err)
	}
	// The dashboard path is unchanged in behavior.
	sess := map[string]any{"sub": "user_1", "org_id": "org_9", "exp": float64(time.Now().Add(time.Hour).Unix())}
	jc, err := validateToken(signTestJWT(t, key, kid, sess))
	if err != nil || jc.Sub != "user_1" || jc.OrgID != "org_9" {
		t.Fatalf("validateToken = %+v, %v", jc, err)
	}
}

// A badly-typed MCP-only claim must never break the dashboard path: it reads
// none of these fields, so it must decode exactly as it would have before
// verifyJWT started reading them at all.
//
// The MCP path must reject it, and reject it FOR the decode failure. Every
// other claim in each payload here is valid, so the token would otherwise
// sail through: a claim that fails to decode is left at its zero value, and
// for sid and nbf the zero value is the permissive reading. `"sid": 42` is a
// session token that no longer looks like one, and `"nbf": "x"` is a
// not-yet-valid token whose not-before check is skipped. Treating "did not
// decode" as "not present" makes a malformed credential outrank a
// well-formed one, so each case asserts the claim's own name in the reason,
// not merely that something was refused.
func TestVerifyJWTToleratesMalformedMCPOnlyClaims(t *testing.T) {
	key, kid := testJWKS(t)
	s := mcpTestServer()
	// Each payload is a fully valid MCP access token except for one claim,
	// which isolates the rejection to that claim.
	withClaim := func(name string, value any) map[string]any {
		c := accessTokenClaims()
		c[name] = value
		return c
	}
	tests := []struct {
		name    string
		payload map[string]any
		reason  string
	}{
		{"non-string aud", withClaim("aud", 123), "malformed claim aud"},
		{"non-string sid", withClaim("sid", 42), "malformed claim sid"},
		{"non-numeric nbf", withClaim("nbf", "x"), "malformed claim nbf"},
		{"non-string scope", withClaim("scope", 7), "malformed claim scope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := signTestJWT(t, key, kid, tt.payload)
			jc, err := validateToken(raw)
			if err != nil || jc.Sub != "user_1" || jc.OrgID != "org_1" {
				t.Fatalf("validateToken = %+v, %v, want sub/org_id intact", jc, err)
			}
			_, err = s.mcpTokenVerifier(context.Background(), raw, nil)
			if !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("mcpTokenVerifier err = %v, want ErrInvalidToken", err)
			}
			if !strings.Contains(err.Error(), tt.reason) {
				t.Fatalf("mcpTokenVerifier err = %q, want it to name the malformed claim (%q)", err, tt.reason)
			}
		})
	}

	// The same tokens minus the malformation are accepted, so the rejections
	// above are attributable to the malformed claim and nothing else.
	if _, err := s.mcpTokenVerifier(context.Background(), signTestJWT(t, key, kid, accessTokenClaims()), nil); err != nil {
		t.Fatalf("well-formed token: %v", err)
	}
}

// A malformed claim earlier in the payload's byte order must not blank out a
// well-typed claim later in it: json.Unmarshal into a struct stops decoding
// remaining fields once one field's custom UnmarshalJSON errors, so verifyJWT
// must decode each MCP-only claim independently rather than as one struct.
func TestVerifyJWTMalformedAudDoesNotSuppressLaterClaims(t *testing.T) {
	key, kid := testJWKS(t)
	claims := accessTokenClaims()
	claims["aud"] = 123 // malformed; org_role and scope both follow it below
	c, err := verifyJWT(signTestJWT(t, key, kid, claims))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Aud) != 0 {
		t.Fatalf("aud = %v, want empty (malformed claim treated as absent)", c.Aud)
	}
	if c.OrgRole != "org:member" || c.Scope == "" {
		t.Fatalf("claims = %+v, want org_role/scope intact despite malformed aud", c)
	}
}

// null and blank values must decode as absent, not as a present-but-empty
// claim: json.Unmarshal of null into a string target succeeds silently, and
// without stripping empty strings a null aud/scp would defeat the checks
// that treat an empty claim as missing.
func TestVerifyJWTNullAudAndScpFallBackToScopes(t *testing.T) {
	key, kid := testJWKS(t)
	claims := accessTokenClaims()
	claims["aud"] = nil
	claims["scp"] = nil
	claims["scopes"] = []string{"argus:read"}
	c, err := verifyJWT(signTestJWT(t, key, kid, claims))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Aud) != 0 {
		t.Fatalf("aud = %v, want empty for null", c.Aud)
	}
	if len(c.Scp) != 1 || c.Scp[0] != "argus:read" {
		t.Fatalf("scp = %v, want [argus:read] (null scp falls back to scopes)", c.Scp)
	}
}

func TestGrantedScopes(t *testing.T) {
	t.Parallel()
	if got := grantedScopes(decodedClaims{Scope: "a  b c"}); len(got) != 3 || got[2] != "c" {
		t.Fatalf("scope string: %v", got)
	}
	if got := grantedScopes(decodedClaims{Scp: []string{"x", "y"}}); len(got) != 2 {
		t.Fatalf("scp array: %v", got)
	}
	if got := grantedScopes(decodedClaims{}); len(got) != 0 {
		t.Fatalf("none: %v", got)
	}
}

func TestMCPTokenVerifierPolicy(t *testing.T) {
	key, kid := testJWKS(t)
	s := mcpTestServer()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://evil.example" }},
		{"wrong audience", func(c map[string]any) { c["aud"] = "https://other/mcp" }},
		{"missing audience", func(c map[string]any) { delete(c, "aud") }},
		{"expired", func(c map[string]any) { c["exp"] = float64(time.Now().Add(-time.Minute).Unix()) }},
		{"not yet valid", func(c map[string]any) { c["nbf"] = float64(time.Now().Add(time.Hour).Unix()) }},
		{"missing subject", func(c map[string]any) { delete(c, "sub") }},
		{"missing org", func(c map[string]any) { delete(c, "org_id") }},
		{"session token (sid)", func(c map[string]any) { c["sid"] = "sess_123" }},
		{"no scopes", func(c map[string]any) { delete(c, "scope") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := accessTokenClaims()
			tt.mutate(claims)
			_, err := s.mcpTokenVerifier(context.Background(), signTestJWT(t, key, kid, claims), nil)
			if err == nil || !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("err = %v, want ErrInvalidToken", err)
			}
		})
	}

	info, err := s.mcpTokenVerifier(context.Background(), signTestJWT(t, key, kid, accessTokenClaims()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.UserID != "user_1" || info.Extra["org_id"] != "org_1" || info.Extra["org_role"] != "org:member" || len(info.Scopes) != 3 {
		t.Fatalf("TokenInfo = %+v", info)
	}
	// An opaque token is not a JWT and must be rejected the same way.
	if _, err := s.mcpTokenVerifier(context.Background(), "oat_opaque_token_value", nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("opaque token: err = %v", err)
	}
}

// A `scope` claim that is present but blank must fall back to `scp`, not be
// trusted as "no scopes granted": strings.Fields("   ") is empty, and
// grantedScopes must not stop there when scp carries real scopes.
func TestMCPTokenVerifierWhitespaceScopeFallsBackToScp(t *testing.T) {
	key, kid := testJWKS(t)
	s := mcpTestServer()

	rejectClaims := accessTokenClaims()
	rejectClaims["scope"] = "   "
	if _, err := s.mcpTokenVerifier(context.Background(), signTestJWT(t, key, kid, rejectClaims), nil); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("whitespace scope, no scp: err = %v, want ErrInvalidToken", err)
	}

	acceptClaims := accessTokenClaims()
	acceptClaims["scope"] = "   "
	acceptClaims["scp"] = []string{"argus:read"}
	info, err := s.mcpTokenVerifier(context.Background(), signTestJWT(t, key, kid, acceptClaims), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Scopes) != 1 || info.Scopes[0] != "argus:read" {
		t.Fatalf("scopes = %v, want [argus:read]", info.Scopes)
	}
}

func TestMCPClaimsToContext(t *testing.T) {
	t.Parallel()
	var got struct {
		user, org, role string
		scopes          []string
	}
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.user, got.org, got.role = getUserID(r.Context()), getOrgID(r.Context()), getOrgRole(r.Context())
		got.scopes = getMCPScopes(r.Context())
	})
	stub := func(context.Context, string, *http.Request) (*auth.TokenInfo, error) {
		// Expiration must be set: auth.RequireBearerToken unconditionally 401s
		// a zero-value Expiration before this test's inner handler ever runs.
		return &auth.TokenInfo{UserID: "user_7", Scopes: []string{scopeRead}, Expiration: time.Now().Add(time.Hour), Extra: map[string]any{"org_id": "org_3", "org_role": "org:member"}}, nil
	}
	h := auth.RequireBearerToken(stub, nil)(mcpClaimsToContext(inner))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer any")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.user != "user_7" || got.org != "org_3" || got.role != "org:member" || len(got.scopes) != 1 || got.scopes[0] != scopeRead {
		t.Fatalf("context = %+v", got)
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	t.Parallel()
	s := mcpTestServer()
	rec := httptest.NewRecorder()
	s.handleProtectedResourceMetadata(rec, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Resource               string   `json:"resource"`
		AuthorizationServers   []string `json:"authorization_servers"`
		ScopesSupported        []string `json:"scopes_supported"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Resource != testResource || len(body.AuthorizationServers) != 1 || body.AuthorizationServers[0] != testIssuer {
		t.Fatalf("body = %+v", body)
	}
	if strings.Join(body.ScopesSupported, " ") != "argus:read argus:memory:write user:org:read" || len(body.BearerMethodsSupported) != 1 || body.BearerMethodsSupported[0] != "header" {
		t.Fatalf("body = %+v", body)
	}
	if got := s.mcpResourceMetadataURL(); got != "https://api.example/.well-known/oauth-protected-resource" {
		t.Fatalf("metadata url = %q", got)
	}
}

// End-to-end through the SDK middleware: no token yields a 401 whose
// WWW-Authenticate points at our metadata document; a valid token reaches the
// handler with the user id in the context the rest of the api package reads.
func TestMCPAuthChainChallengeAndPassThrough(t *testing.T) {
	key, kid := testJWKS(t)
	s := mcpTestServer()
	var seenUser string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUser = getUserID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	h := auth.RequireBearerToken(s.mcpTokenVerifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: s.mcpResourceMetadataURL(),
	})(mcpClaimsToContext(inner))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
	if www := rec.Header().Get("WWW-Authenticate"); !strings.Contains(www, "resource_metadata=") || !strings.Contains(www, "/.well-known/oauth-protected-resource") {
		t.Fatalf("WWW-Authenticate = %q, want a resource_metadata challenge", www)
	}

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, key, kid, accessTokenClaims()))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid token: status = %d body=%s", rec.Code, rec.Body.String())
	}
	if seenUser != "user_1" {
		t.Fatalf("handler saw user %q, want user_1", seenUser)
	}
}
