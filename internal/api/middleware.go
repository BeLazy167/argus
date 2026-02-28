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

	resp, err := http.Get(c.url)
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

// jwtAuth validates JWTs via JWKS. Works with both Clerk and SuperTokens.
// Bypasses auth in dev mode if JWKS URL not configured.
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
		token := strings.TrimPrefix(auth, "Bearer ")

		parts := strings.Split(token, ".")
		if len(parts) != 3 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token format"})
			return
		}

		headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token header"})
			return
		}
		var header struct {
			Kid string `json:"kid"`
			Alg string `json:"alg"`
		}
		if err := json.Unmarshal(headerBytes, &header); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token header"})
			return
		}

		if header.Alg != "RS256" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unsupported signing algorithm"})
			return
		}

		pubKey, err := cache.getKey(header.Kid)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unknown signing key"})
			return
		}

		sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
			return
		}

		hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], sigBytes); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid signature"})
			return
		}

		payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token payload"})
			return
		}
		var claims struct {
			Sub string  `json:"sub"`
			Exp float64 `json:"exp"`
		}
		if err := json.Unmarshal(payloadBytes, &claims); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid claims"})
			return
		}

		if time.Now().Unix() > int64(claims.Exp) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token expired"})
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, claims.Sub)
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
		ids, err := s.store.GetUserInstallationIDs(r.Context(), userID)
		if err != nil {
			s.logger.Error("get user installation ids", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if len(ids) == 0 {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "no installations linked"})
			return
		}
		if h := r.Header.Get("X-Installation-ID"); h != "" {
			reqID, err := strconv.ParseInt(h, 10, 64)
			if err == nil {
				for _, id := range ids {
					if id == reqID {
						ids = []int64{reqID}
						break
					}
				}
			}
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
