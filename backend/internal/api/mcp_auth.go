package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// OAuth scopes the MCP surface enforces. Defined as custom scopes in Clerk and
// advertised in protected-resource metadata; read tools need scopeRead, the
// three memory mutations need scopeMemoryWrite. user:org:read is Clerk's own
// scope that makes the consent screen ask which org the token is for.
const (
	scopeRead        = "argus:read"
	scopeMemoryWrite = "argus:memory:write"
	scopeOrgRead     = "user:org:read"
)

// nbfSkew is the clock tolerance applied to `nbf`, matching Clerk's SDKs.
const nbfSkew = 5 * time.Second

type mcpContextKey string

const mcpScopesKey mcpContextKey = "mcp_scopes"

// grantedScopes reads the token's granted scopes. Clerk does not document the
// claim name for JWT access tokens, so both standard encodings are accepted:
// RFC 8693/9068 `scope` (space-delimited) and an `scp`/`scopes` array. Fields
// splits on whitespace, so a `scope` claim that is present but blank (e.g.
// "   ") yields no scopes rather than a claim to trust — fall back to c.Scp
// rather than returning that empty result. The OAuth acceptance run confirms
// the real shape; this is the one place to adjust if it differs.
func grantedScopes(c decodedClaims) []string {
	if scopes := strings.Fields(c.Scope); len(scopes) > 0 {
		return scopes
	}
	return c.Scp
}

// mcpResourceMetadataURL is where RequireBearerToken points a 401 challenge:
// the RFC 9728 well-known document at the resource's origin.
func (s *Server) mcpResourceMetadataURL() string {
	u, err := url.Parse(s.cfg.MCPResourceURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource"
}

// mcpTokenVerifier is the ONLY token verifier the MCP path uses, adapting the
// existing JWKS verifier to the SDK's auth.TokenVerifier and adding the
// MCP-specific policy the dashboard path does not need:
//
//   - iss pinned to the configured Clerk issuer;
//   - aud must contain the configured resource URL (confused-deputy
//     protection — a token minted for another API must not work here);
//   - nbf honored; sub and org_id required (org selection is what scopes the
//     connection to one tenant);
//   - a `sid` claim marks a Clerk session token, which is not an MCP
//     credential; opaque tokens fail structural parsing;
//   - every MCP-only claim present in the payload must actually decode: a
//     badly-typed sid or nbf otherwise reads as an absent claim and skips
//     the very check it should trigger;
//   - at least one granted scope, copied into TokenInfo.Scopes.
//
// Every rejection wraps auth.ErrInvalidToken so the SDK emits a 401 with the
// resource_metadata challenge, which is what tells a client to (re)authorize.
func (s *Server) mcpTokenVerifier(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	c, err := verifyJWT(token)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", auth.ErrInvalidToken, err.Error())
	}
	// Checked before every value check below: a claim that failed to decode
	// is sitting at its zero value, so those checks would be reading absence
	// where the token actually carries something this build cannot read.
	if len(c.Malformed) > 0 {
		return nil, fmt.Errorf("%w: malformed claim %s", auth.ErrInvalidToken, strings.Join(c.Malformed, ", "))
	}
	if c.Sid != "" {
		return nil, fmt.Errorf("%w: session tokens are not accepted", auth.ErrInvalidToken)
	}
	if c.Sub == "" {
		return nil, fmt.Errorf("%w: missing subject", auth.ErrInvalidToken)
	}
	if c.Iss != s.cfg.ClerkIssuerURL {
		return nil, fmt.Errorf("%w: issuer mismatch", auth.ErrInvalidToken)
	}
	if !slices.Contains(c.Aud, s.cfg.MCPResourceURL) {
		return nil, fmt.Errorf("%w: audience mismatch", auth.ErrInvalidToken)
	}
	if c.Nbf > 0 && time.Now().Add(nbfSkew).Unix() < int64(c.Nbf) {
		return nil, fmt.Errorf("%w: token not yet valid", auth.ErrInvalidToken)
	}
	if c.OrgID == "" {
		return nil, fmt.Errorf("%w: organization selection required (authorize with scope %s)", auth.ErrInvalidToken, scopeOrgRead)
	}
	scopes := grantedScopes(c)
	if len(scopes) == 0 {
		return nil, fmt.Errorf("%w: token carries no scopes", auth.ErrInvalidToken)
	}
	return &auth.TokenInfo{
		UserID:     c.Sub,
		Scopes:     scopes,
		Expiration: time.Unix(int64(c.Exp), 0),
		Extra:      map[string]any{"org_id": c.OrgID, "org_role": c.OrgRole},
	}, nil
}

// mcpClaimsToContext copies the verified TokenInfo into the context keys the
// rest of the api package reads (getUserID/getOrgID/getOrgRole) plus the
// granted scopes, so requireMCPInstallationScope and mcpGetServer need no
// SDK types.
func mcpClaimsToContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := auth.TokenInfoFromContext(r.Context())
		if info == nil || info.UserID == "" {
			// RequireBearerToken already rejected; defensive against a
			// misordered chain.
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, info.UserID)
		ctx = context.WithValue(ctx, mcpScopesKey, info.Scopes)
		if org, _ := info.Extra["org_id"].(string); org != "" {
			ctx = context.WithValue(ctx, orgIDKey, org)
		}
		if role, _ := info.Extra["org_role"].(string); role != "" {
			ctx = context.WithValue(ctx, orgRoleKey, role)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getMCPScopes(ctx context.Context) []string {
	scopes, _ := ctx.Value(mcpScopesKey).([]string)
	return scopes
}

// protectedResourceMetadata is the RFC 9728 document MCP clients fetch to
// discover which authorization server to obtain a token from and which
// scopes to ask for.
type protectedResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
}

func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	op := s.beginOperation(r.Context(), "api.mcp.protected_resource_metadata")
	defer op.Finish(w)
	writeJSON(w, http.StatusOK, protectedResourceMetadata{
		Resource:               s.cfg.MCPResourceURL,
		AuthorizationServers:   []string{s.cfg.ClerkIssuerURL},
		ScopesSupported:        []string{scopeRead, scopeMemoryWrite, scopeOrgRead},
		BearerMethodsSupported: []string{"header"},
	})
}

// requireMCPInstallationScope confines the request to the org the user
// selected during OAuth consent.
//
// It reuses resolveInstallationIDs' org branch — which maps org_id to the
// linked installation and auto-links the user — but never its user-wide
// fallback: a missing org claim is a 401 here, not a lookup of everything the
// user can see. The REST middleware silently ignores a malformed or
// non-member X-Installation-ID and returns the full set (middleware.go:229);
// this wrapper rejects both, so a hint can only ever narrow within the org.
func (s *Server) requireMCPInstallationScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := s.beginOperation(r.Context(), "middleware.mcp_installation_scope")
		defer op.Finish(w)
		userID, orgID := getUserID(r.Context()), getOrgID(r.Context())
		if userID == "" || orgID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "organization selection required"})
			return
		}
		// The hint is parsed BEFORE resolution because resolveInstallationIDs
		// writes: its org branch auto-links the user to the org's
		// installation. A request carrying an unparsable hint is rejected
		// outright, so it must not get that side effect first — validate what
		// the request alone can settle, then resolve.
		hint := r.Header.Get("X-Installation-ID")
		var hintID int64
		if hint != "" {
			id, perr := strconv.ParseInt(hint, 10, 64)
			if perr != nil {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "installation hint conflicts with the selected organization"})
				return
			}
			hintID = id
		}
		claims := jwtClaims{Sub: userID, OrgID: orgID, OrgRole: getOrgRole(r.Context())}
		ids, err := s.resolveInstallationIDs(r.Context(), claims, "")
		if err != nil {
			s.logger.WarnContext(r.Context(), "mcp installation scope", "user", userID, "org", orgID, "error", err)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "no installation linked to the selected organization"})
			return
		}
		// Membership, unlike syntax, needs the resolved set, so it stays here.
		if hint != "" {
			if !containsID(ids, hintID) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "installation hint conflicts with the selected organization"})
				return
			}
			ids = []int64{hintID}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), installationIDsKey, ids)))
	})
}
