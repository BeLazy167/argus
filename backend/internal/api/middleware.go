package api

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

type contextKey string

const userIDKey contextKey = "user_id"
const installationIDsKey contextKey = "installation_ids"
const orgIDKey contextKey = "org_id"
const orgRoleKey contextKey = "org_role"

type jwks struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

type jwksCache struct {
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	url       string
	logger    *slog.Logger
}

var cache *jwksCache

// InitJWKS sets the JWKS URL for JWT verification (works with Clerk or SuperTokens).
func InitJWKS(url string, logger *slog.Logger) {
	cache = &jwksCache{url: url, keys: make(map[string]*rsa.PublicKey), logger: logger}
}

func (c *jwksCache) getKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	if key, ok := c.keys[kid]; ok && time.Since(c.fetchedAt) < 10*time.Minute {
		c.mu.RUnlock()
		return key, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if key, ok := c.keys[kid]; ok && time.Since(c.fetchedAt) < 10*time.Minute {
		return key, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating JWKS request: %w", err)
	}
	resp, err := (&http.Client{Transport: obs.NewLoggingRoundTripper("jwks", http.DefaultTransport), Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS endpoint returned status %d", resp.StatusCode)
	}

	var ks jwks
	if err := json.NewDecoder(resp.Body).Decode(&ks); err != nil {
		return nil, fmt.Errorf("decoding JWKS: %w", err)
	}

	c.keys = make(map[string]*rsa.PublicKey, len(ks.Keys))
	for _, k := range ks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := parseRSAPublicKey(k)
		if err != nil {
			c.logger.Warn("skipping JWKS key: parse failed", "kid", k.Kid, "error", err)
			continue
		}
		c.keys[k.Kid] = pub
	}
	c.fetchedAt = time.Now()

	if key, ok := c.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("key %s not found in JWKS", kid)
}

func parseRSAPublicKey(k jwk) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	e := new(big.Int).SetBytes(eb)
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nb),
		E: int(e.Int64()),
	}, nil
}

type jwtClaims struct {
	Sub     string
	OrgID   string
	OrgRole string
}

// decodedClaims is everything verifyJWT reads from a verified payload. The
// dashboard path (validateToken) uses sub/org_id/org_role; the MCP path
// additionally pins iss/aud, checks nbf, and reads sid and the scope claims.
// Decoding is shared; policy is not — the dashboard's acceptance rules are
// deliberately unchanged.
type decodedClaims struct {
	Sub     string
	Iss     string
	Aud     []string
	OrgID   string
	OrgRole string
	Sid     string
	Scope   string
	Scp     []string
	Exp     float64
	Nbf     float64
	// Malformed names the MCP-only claims that were present in the payload
	// but failed to decode, in a fixed field order. Each such claim is left
	// at its zero value, which is indistinguishable from an absent claim —
	// so without this record a non-string `sid` would read as "no session
	// token" and a non-numeric `nbf` as "no not-before", turning a malformed
	// credential into a more permissive one. The MCP path rejects on any
	// entry; the dashboard path ignores it, keeping validateToken's
	// acceptance rules unchanged.
	Malformed []string
}

// stringOrArray accepts a JSON string or array of strings (RFC 7519 `aud`;
// also the `scp`/`scopes` claim shapes). JSON null and empty strings never
// produce an element: unmarshaling a plain string field from JSON null
// leaves it at its zero value ("") without an error, so without this a
// null-valued claim would decode as [""] instead of an absent claim — and an
// empty string in an array would defeat an emptiness check the same way.
type stringOrArray []string

func (a *stringOrArray) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*a = nil
		return nil
	}
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		if one == "" {
			*a = nil
			return nil
		}
		*a = stringOrArray{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	out := make(stringOrArray, 0, len(many))
	for _, s := range many {
		if s != "" {
			out = append(out, s)
		}
	}
	*a = out
	return nil
}

// verifyJWT checks the token's structure, RS256 signature against the JWKS
// cache, and expiry, then returns the decoded claims. Every error string is a
// fixed literal — jwtAuth echoes them to the client.
func verifyJWT(raw string) (decodedClaims, error) {
	if cache == nil || cache.url == "" {
		return decodedClaims{}, fmt.Errorf("JWKS not configured")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return decodedClaims{}, fmt.Errorf("invalid token format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return decodedClaims{}, fmt.Errorf("invalid token header")
	}
	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return decodedClaims{}, fmt.Errorf("invalid token header")
	}
	if header.Alg != "RS256" {
		return decodedClaims{}, fmt.Errorf("unsupported signing algorithm")
	}
	pubKey, err := cache.getKey(header.Kid)
	if err != nil {
		return decodedClaims{}, fmt.Errorf("unknown signing key")
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return decodedClaims{}, fmt.Errorf("invalid signature")
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], sigBytes); err != nil {
		return decodedClaims{}, fmt.Errorf("invalid signature")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return decodedClaims{}, fmt.Errorf("invalid token payload")
	}
	// The four dashboard claims decode with the same struct and the same
	// hard failure validateToken has always had: a badly-typed sub, exp,
	// org_id, or org_role is a real "invalid claims" rejection.
	var claims struct {
		Sub     string  `json:"sub"`
		Exp     float64 `json:"exp"`
		OrgID   string  `json:"org_id"`
		OrgRole string  `json:"org_role"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return decodedClaims{}, fmt.Errorf("invalid claims")
	}
	if time.Now().Unix() > int64(claims.Exp) {
		return decodedClaims{}, fmt.Errorf("token expired")
	}

	// The MCP-only claims are decoded one field at a time from the same
	// payload, not as a second struct decoded in one call: json.Unmarshal
	// stops decoding a struct's remaining fields as soon as one field's
	// UnmarshalJSON returns an error, so a single malformed claim (say a
	// non-string aud) would otherwise blank out every sibling claim that
	// happens to sit later in the token's byte order too — not just the bad
	// one. Decoding each claim from its own isolated raw slice makes a
	// malformed claim's failure affect only that claim: it is simply left at
	// its zero value.
	//
	// A zero value is not "absent", though, so each failure is also recorded
	// by name in Malformed. The MCP path refuses a token with any entry: for
	// sid and nbf a silent zero value is strictly more permissive than the
	// real claim, so treating a decode failure as absence would let a
	// malformed token past a check a well-formed one fails. The dashboard
	// path never reads Malformed, so its acceptance rules are unchanged.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(payloadBytes, &fields)
	var mcp struct {
		Iss    string
		Aud    stringOrArray
		Nbf    float64
		Sid    string
		Scope  string
		Scp    stringOrArray
		Scopes stringOrArray
	}
	var malformed []string
	decodeClaim := func(name string, dst any) {
		v, ok := fields[name]
		if !ok {
			return
		}
		if err := json.Unmarshal(v, dst); err != nil {
			malformed = append(malformed, name)
		}
	}
	decodeClaim("iss", &mcp.Iss)
	decodeClaim("aud", &mcp.Aud)
	decodeClaim("nbf", &mcp.Nbf)
	decodeClaim("sid", &mcp.Sid)
	decodeClaim("scope", &mcp.Scope)
	decodeClaim("scp", &mcp.Scp)
	decodeClaim("scopes", &mcp.Scopes)

	scp := []string(mcp.Scp)
	if len(scp) == 0 {
		scp = []string(mcp.Scopes)
	}
	return decodedClaims{
		Sub: claims.Sub, Iss: mcp.Iss, Aud: []string(mcp.Aud), OrgID: claims.OrgID, OrgRole: claims.OrgRole,
		Sid: mcp.Sid, Scope: mcp.Scope, Scp: scp, Exp: claims.Exp, Nbf: mcp.Nbf,
		Malformed: malformed,
	}, nil
}

// validateToken parses and verifies a JWT, returning the claims the dashboard
// API uses. It is verifyJWT minus the MCP-only fields; its checks and error
// strings are unchanged.
func validateToken(raw string) (jwtClaims, error) {
	c, err := verifyJWT(raw)
	if err != nil {
		return jwtClaims{}, err
	}
	return jwtClaims{Sub: c.Sub, OrgID: c.OrgID, OrgRole: c.OrgRole}, nil
}

// resolveInstallationIDs resolves the installation IDs a user has access to.
// When resolving via org scope, it also auto-creates a user_installations row
// so that /api/v1/me/installations and other user-scoped queries work correctly.
func (s *Server) resolveInstallationIDs(ctx context.Context, claims jwtClaims, installationIDHint string) ([]int64, error) {
	var ids []int64
	if claims.OrgID != "" {
		inst, err := s.store.GetInstallationByClerkOrgID(ctx, claims.OrgID)
		if err == nil {
			ids = []int64{inst.ID}
			// Ensure the user has a user_installations row for this org's installation.
			// Check first — LinkUserInstallation does an ON CONFLICT UPSERT, so
			// calling it unconditionally costs a write per request. Most requests
			// already have the link, so we skip the write on the hot path.
			alreadyLinked, checkErr := s.store.IsUserLinkedToInstallation(ctx, claims.Sub, inst.ID)
			if checkErr != nil {
				s.logger.WarnContext(ctx, "auto-link precheck failed", "user", claims.Sub, "installation", inst.ID, "error", checkErr)
			} else if !alreadyLinked {
				role := claims.OrgRole
				if role == "" {
					role = "org_member"
				}
				if _, linkErr := s.store.LinkUserInstallation(ctx, claims.Sub, inst.ID, role); linkErr != nil {
					s.logger.WarnContext(ctx, "auto-link user to org installation failed", "user", claims.Sub, "installation", inst.ID, "error", linkErr)
				}
			}
		} else {
			// Org has no linked installation — return empty, don't leak other orgs' data
			return nil, fmt.Errorf("no installations linked to this organization")
		}
	} else {
		var err error
		ids, err = s.store.GetUserInstallationIDs(ctx, claims.Sub)
		if err != nil {
			return nil, fmt.Errorf("resolving installation IDs: %w", err)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no installations linked")
	}
	if installationIDHint != "" {
		reqID, err := strconv.ParseInt(installationIDHint, 10, 64)
		if err == nil {
			for _, id := range ids {
				if id == reqID {
					return []int64{reqID}, nil
				}
			}
		}
	}
	return ids, nil
}

// jwtAuth validates JWTs via JWKS. Works with both Clerk and SuperTokens.
func (s *Server) jwtAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := s.beginOperation(r.Context(), "middleware.jwt_auth")
		defer op.Finish(w)
		if cache == nil || cache.url == "" {
			s.logger.ErrorContext(r.Context(), "JWT auth unavailable: JWKS URL not configured")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "authentication not configured"})
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing authorization"})
			return
		}
		claims, err := validateToken(strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, claims.Sub)
		if claims.OrgID != "" {
			ctx = context.WithValue(ctx, orgIDKey, claims.OrgID)
		}
		if claims.OrgRole != "" {
			ctx = context.WithValue(ctx, orgRoleKey, claims.OrgRole)
		}
		// Attribute PostHog events on this request to the Clerk user so the
		// Handler's resolveDistinctID falls through to ClerkUser(ctx) instead
		// of the "unattributed" bucket.
		if claims.Sub != "" {
			ctx = obs.SetClerkUser(ctx, claims.Sub)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireInstallationScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := s.beginOperation(r.Context(), "middleware.installation_scope")
		defer op.Finish(w)
		userID := getUserID(r.Context())
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		claims := jwtClaims{Sub: userID, OrgID: getOrgID(r.Context()), OrgRole: getOrgRole(r.Context())}
		ids, err := s.resolveInstallationIDs(r.Context(), claims, r.Header.Get("X-Installation-ID"))
		if err != nil {
			s.logger.ErrorContext(r.Context(), "resolving installation scope", "error", err)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		}
		ctx := context.WithValue(r.Context(), installationIDsKey, ids)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getInstallationIDs(ctx context.Context) []int64 {
	ids, _ := ctx.Value(installationIDsKey).([]int64)
	return ids
}

func getUserID(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey).(string)
	return id
}

func getOrgID(ctx context.Context) string {
	id, _ := ctx.Value(orgIDKey).(string)
	return id
}

func getOrgRole(ctx context.Context) string {
	role, _ := ctx.Value(orgRoleKey).(string)
	return role
}

// cors adds CORS headers for the frontend origin.
// allowOrigin can be comma-separated for multiple origins.
func cors(allowOrigin string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool)
	for _, o := range strings.Split(allowOrigin, ",") {
		allowed[strings.TrimSpace(o)] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Installation-ID, X-Argus-Trace-Id")
			w.Header().Set("Access-Control-Expose-Headers", "X-Argus-Trace-Id")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
