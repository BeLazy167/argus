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
	resp, err := http.DefaultClient.Do(req)
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

// validateToken parses and verifies a JWT, returning claims.
func validateToken(raw string) (jwtClaims, error) {
	if cache == nil || cache.url == "" {
		return jwtClaims{}, fmt.Errorf("JWKS not configured")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return jwtClaims{}, fmt.Errorf("invalid token format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("invalid token header")
	}
	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return jwtClaims{}, fmt.Errorf("invalid token header")
	}
	if header.Alg != "RS256" {
		return jwtClaims{}, fmt.Errorf("unsupported signing algorithm")
	}
	pubKey, err := cache.getKey(header.Kid)
	if err != nil {
		return jwtClaims{}, fmt.Errorf("unknown signing key")
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("invalid signature")
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], sigBytes); err != nil {
		return jwtClaims{}, fmt.Errorf("invalid signature")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, fmt.Errorf("invalid token payload")
	}
	var claims struct {
		Sub     string  `json:"sub"`
		Exp     float64 `json:"exp"`
		OrgID   string  `json:"org_id"`
		OrgRole string  `json:"org_role"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return jwtClaims{}, fmt.Errorf("invalid claims")
	}
	if time.Now().Unix() > int64(claims.Exp) {
		return jwtClaims{}, fmt.Errorf("token expired")
	}
	return jwtClaims{Sub: claims.Sub, OrgID: claims.OrgID, OrgRole: claims.OrgRole}, nil
}

// resolveInstallationIDs resolves the installation IDs a user has access to.
func (s *Server) resolveInstallationIDs(ctx context.Context, claims jwtClaims, installationIDHint string) ([]int64, error) {
	var ids []int64
	if claims.OrgID != "" {
		inst, err := s.store.GetInstallationByClerkOrgID(ctx, claims.OrgID)
		if err == nil {
			ids = []int64{inst.ID}
		} else {
			ids, err = s.store.GetUserInstallationIDs(ctx, claims.Sub)
			if err != nil {
				return nil, fmt.Errorf("resolving installation IDs: %w", err)
			}
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
		if cache == nil || cache.url == "" {
			s.logger.Warn("JWT auth bypassed: JWKS URL not configured")
			next.ServeHTTP(w, r)
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
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireInstallationScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := getUserID(r.Context())
		if userID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		claims := jwtClaims{Sub: userID, OrgID: getOrgID(r.Context()), OrgRole: getOrgRole(r.Context())}
		ids, err := s.resolveInstallationIDs(r.Context(), claims, r.Header.Get("X-Installation-ID"))
		if err != nil {
			s.logger.Error("resolving installation scope", "error", err)
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
func cors(allowOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Installation-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
