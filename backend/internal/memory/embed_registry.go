package memory

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"
)

// EmbeddingsProvider is the provider_keys slot for a customer-supplied
// embeddings key. Embeddings are deliberately installation-wide (never
// repo-scoped): an installation has exactly one embedding space —
// memories.embedding_space and the similarity floors are calibrated per
// space — so per-repo keys would fragment retrieval. The provider-key API
// rejects repo-scoped rows for this slot.
const EmbeddingsProvider = "embeddings"

// Hosted endpoints that REQUIRE an API key; any other base URL
// (Ollama/TEI/self-hosted gateways) may legitimately be keyless.
const (
	defaultVoyageBaseURL     = "https://api.voyageai.com/v1"
	defaultOpenAIBaseURL     = "https://api.openai.com/v1"
	defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	defaultVercelGatewayBase = "https://ai-gateway.vercel.sh/v1"
)

// HostedKeyedBase reports whether base is a known hosted endpoint that cannot
// work without an API key (trailing-slash and host-case tolerant). Exported so
// the provider-key API can reject keyless rows that point at these bases at
// save time instead of silently disabling embeddings at resolve time.
func HostedKeyedBase(base string) bool {
	b := NormalizeBaseURL(base)
	return b == defaultVoyageBaseURL || b == defaultOpenAIBaseURL ||
		b == defaultOpenRouterBaseURL || b == defaultVercelGatewayBase
}

// NormalizeBaseURL canonicalizes a base URL: trailing slash trimmed, scheme
// and host lowercased. DNS is case-insensitive, so
// "https://API.voyageai.com/v1" IS the Voyage endpoint — without this, a
// case-variant base slips past both the hosted-key requirement and the
// catalog check and saves a row that 401s at request time. Path case is
// preserved (paths are case-sensitive); unparseable input falls back to the
// trimmed string so comparisons stay exact-match rather than panicking.
// Exported so the save handler persists the SAME canonical form it validates
// — a stored variant would render as "Custom endpoint" in the card, whose
// provider inference exact-matches catalog bases.
func NormalizeBaseURL(base string) string {
	b := strings.TrimRight(base, "/")
	u, err := url.Parse(b)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return b
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		u.Host = host
	}
	return u.String()
}

// embedderCacheTTL bounds staleness after a key/model change, mirroring
// llm.Registry's 5-minute provider cache. The provider-key handlers call
// Invalidate on upsert and delete, so a key change refreshes immediately in
// the process that served the request; the TTL bounds staleness on every OTHER
// machine, which is what a rotation actually has to wait out.
const embedderCacheTTL = 5 * time.Minute

// errorEmbedderTTL bounds how long a FAILED resolve's platform fallback is
// published. Deliberately its own constant rather than borrowing the flag
// cache's: the two govern independent policies, and tuning the flag-read retry
// window to damp log volume during an outage must not silently retune how long
// rows are stamped with an unreadable embedding_model.
const errorEmbedderTTL = 10 * time.Second

// EmbedKeyResolver is the narrow store seam the registry needs — satisfied by
// *store.Store.ResolveEmbeddingsKey (installation-wide row; model "" when the
// row doesn't override it).
type EmbedKeyResolver interface {
	ResolveEmbeddingsKey(ctx context.Context, installationID int64) (apiKey, baseURL, model string, found bool, err error)
}

// PlatformEmbeddings is the managed-tier default supplied from config: with a
// platform key set, memory works for every installation regardless of which
// LLM provider they bring. Self-hosters set the env key or point BaseURL at a
// local OpenAI-compatible server.
type PlatformEmbeddings struct {
	APIKey     string
	BaseURL    string
	Model      string
	Dimensions int // memories storage dims (vector(1024))
}

type cachedEmbedder struct {
	embedder  Embedder // nil = embeddings-off, cached too (negative caching)
	expiresAt time.Time
}

// EmbedderRegistry hands out a cached Embedder per installation, resolving the
// BYOK "embeddings" slot first and falling back to the platform key. A nil
// (Embedder, error) return means embeddings are unavailable for the
// installation — mirroring the nil-Indexer memory-off convention, callers
// degrade rather than fail.
type EmbedderRegistry struct {
	resolver EmbedKeyResolver
	platform PlatformEmbeddings
	logger   *slog.Logger

	mu    sync.Mutex
	cache map[int64]cachedEmbedder
	// gen counts invalidations per installation, discarding any resolve that
	// was already in flight when a key changed. Without it, a rotation racing
	// a GetEmbedder leaves the OLD key/model cached for the full TTL — writes
	// then land in a vector space the reader never searches, which is exactly
	// what invalidating on key change is supposed to prevent.
	gen map[int64]uint64
}

// NewEmbedderRegistry wires the registry. platform may be zero-valued (no
// platform key) — then only BYOK installations get embeddings.
func NewEmbedderRegistry(resolver EmbedKeyResolver, platform PlatformEmbeddings, logger *slog.Logger) *EmbedderRegistry {
	if platform.BaseURL == "" {
		platform.BaseURL = defaultVoyageBaseURL
	}
	if platform.Model == "" {
		platform.Model = "voyage-4"
	}
	if platform.Dimensions == 0 {
		platform.Dimensions = 1024
	}
	return &EmbedderRegistry{
		resolver: resolver,
		platform: platform,
		logger:   logger,
		cache:    make(map[int64]cachedEmbedder),
		gen:      make(map[int64]uint64),
	}
}

// Dimensions is the storage dimensionality every embedder from this registry
// produces, after defaulting. It is the single source for PGIndexer's contract
// check: taking the raw config value separately would let an explicit
// EMBEDDINGS_DIMENSIONS=0 default to 1024 here while PGIndexer compared
// against 0, rejecting every vector onto the fail-open NULL path.
func (r *EmbedderRegistry) Dimensions() int { return r.platform.Dimensions }

// GetEmbedder resolves the embedder for an installation: BYOK "embeddings"
// key (with its base_url/model overrides) when configured, else the platform
// key, else nil (embeddings off). Resolution errors fall back to the platform
// key rather than failing the caller — a broken BYOK row should degrade to
// the default, not disable memory outright. Results (including nil) are
// cached for embedderCacheTTL.
func (r *EmbedderRegistry) GetEmbedder(ctx context.Context, installationID int64) (Embedder, error) {
	r.mu.Lock()
	if c, ok := r.cache[installationID]; ok && time.Now().Before(c.expiresAt) {
		r.mu.Unlock()
		return c.embedder, nil
	}
	gen := r.gen[installationID]
	r.mu.Unlock()

	apiKey, baseURL, model := r.platform.APIKey, r.platform.BaseURL, r.platform.Model
	byokKey, byokBase, byokModel, found, err := r.resolver.ResolveEmbeddingsKey(ctx, installationID)
	switch {
	case err != nil:
		r.logger.Warn("embeddings BYOK resolution failed; using platform key",
			"installation_id", installationID, "error", err)
	case found:
		apiKey = byokKey
		if byokBase != "" {
			baseURL = byokBase
		}
		// The API requires model whenever base_url is overridden (custom
		// endpoints serve what they serve, regardless of the request's model
		// field). A BYOK key without overrides means "platform space, my key".
		if byokModel != "" {
			model = byokModel
		}
	}

	var e Embedder
	if apiKey == "" && HostedKeyedBase(baseURL) {
		// No key anywhere and the hosted endpoint requires one: embeddings
		// are off for this installation. Keyless CUSTOM bases (local
		// Ollama/TEI) are legitimate and proceed.
		e = nil
	} else {
		e = NewEmbedder(apiKey, baseURL, model, r.platform.Dimensions)
	}

	// A dead CALLER context says nothing about the installation's key, so
	// publishing this fallback to every other caller would be wrong.
	if ctx.Err() != nil {
		return e, nil
	}

	// A failed resolve substitutes the PLATFORM key and model. Caching that
	// for the full TTL stamps up to five minutes of rows with
	// embedding_space stamped for the platform endpoint while every read gates
	// the score on the installation's real BYOK space — those rows score 0 forever, and because
	// their embedding is NOT NULL. The repair sweep selects the foreign
	// space stamp, but this short TTL still bounds temporary retrieval loss. A short TTL bounds the damage to one retry window.
	ttl := embedderCacheTTL
	if err != nil {
		ttl = errorEmbedderTTL
	}

	r.mu.Lock()
	// Discard the result if a key change invalidated this installation while
	// the resolve was in flight; the next caller re-resolves.
	if r.gen[installationID] == gen {
		r.cache[installationID] = cachedEmbedder{embedder: e, expiresAt: time.Now().Add(ttl)}
	}
	r.mu.Unlock()
	return e, nil
}

// Invalidate drops the cached embedder after a key set/delete so the next
// review picks up the change immediately (same contract as
// Registry.InvalidateClient); the TTL bounds staleness where it isn't called.
func (r *EmbedderRegistry) Invalidate(installationID int64) {
	r.mu.Lock()
	delete(r.cache, installationID)
	r.gen[installationID]++
	r.mu.Unlock()
}
