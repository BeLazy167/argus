package llm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// PipelineStage identifies which stage of the review pipeline a model config applies to.
type PipelineStage string

const (
	StageTriage    PipelineStage = "triage"
	StageReview    PipelineStage = "review"
	StageSynthesis PipelineStage = "synthesis"
	StageEmbedding PipelineStage = "embedding"
)

// ModelConfig holds the LLM configuration for a specific repo + stage.
type ModelConfig struct {
	RepoID      int64
	Stage       PipelineStage
	Provider    string
	Model       string
	MaxTokens   int
	Temperature float64
}

// KeyResolver resolves API keys from the database.
type KeyResolver interface {
	ResolveAPIKey(ctx context.Context, installationID int64, repoID *int64, provider string) (apiKey string, baseURL string, found bool, err error)
}

type cachedProvider struct {
	provider  Provider
	expiresAt time.Time
}

// Registry manages LLM providers and per-repo model configuration.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	defaults  map[PipelineStage]ModelConfig

	resolver     KeyResolver
	cacheMu      sync.RWMutex
	providerCache map[string]cachedProvider
	cacheTTL     time.Duration
}

func NewRegistry() *Registry {
	return &Registry{
		providers:     make(map[string]Provider),
		defaults:      make(map[PipelineStage]ModelConfig),
		providerCache: make(map[string]cachedProvider),
		cacheTTL:      5 * time.Minute,
	}
}

// SetResolver configures the DB-backed key resolver for dynamic provider creation.
func (r *Registry) SetResolver(resolver KeyResolver) {
	r.resolver = resolver
}

func (r *Registry) RegisterProvider(name string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = p
}

func (r *Registry) SetDefault(stage PipelineStage, cfg ModelConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaults[stage] = cfg
}

// GetProvider returns the provider for the given name.
func (r *Registry) GetProvider(name string) (Provider, error) {
	r.mu.RLock()
	p, ok := r.providers[name]
	r.mu.RUnlock()
	if ok {
		return p, nil
	}
	return nil, fmt.Errorf("unknown provider: %s", name)
}

// GetProviderForRepo resolves a provider dynamically, checking DB keys first then falling back to static providers.
func (r *Registry) GetProviderForRepo(ctx context.Context, installationID int64, repoID *int64, providerName string) (Provider, error) {
	if r.resolver == nil {
		return r.GetProvider(providerName)
	}

	cacheKey := fmt.Sprintf("%d:%v:%s", installationID, repoID, providerName)

	r.cacheMu.RLock()
	if cached, ok := r.providerCache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		r.cacheMu.RUnlock()
		return cached.provider, nil
	}
	r.cacheMu.RUnlock()

	apiKey, baseURL, found, err := r.resolver.ResolveAPIKey(ctx, installationID, repoID, providerName)
	if err != nil {
		return nil, fmt.Errorf("resolving api key: %w", err)
	}

	if found {
		if baseURL == "" {
			baseURL = defaultBaseURLForProvider(providerName)
		}
		p := NewChatProvider(providerName, apiKey, baseURL)
		r.cacheMu.Lock()
		r.providerCache[cacheKey] = cachedProvider{provider: p, expiresAt: time.Now().Add(r.cacheTTL)}
		r.cacheMu.Unlock()
		return p, nil
	}

	// Fall back to static provider
	return r.GetProvider(providerName)
}

// GetConfig returns the model config for a repo + stage. Checks repo-specific configs first, then registry defaults.
func (r *Registry) GetConfig(repoID int64, stage PipelineStage, repoConfigs []ModelConfig) ModelConfig {
	// Check repo-specific config first
	for _, cfg := range repoConfigs {
		if cfg.RepoID == repoID && cfg.Stage == stage {
			return cfg
		}
	}
	// Fall back to org default
	r.mu.RLock()
	defer r.mu.RUnlock()
	if def, ok := r.defaults[stage]; ok {
		return def
	}
	// Ultimate fallback
	return ModelConfig{
		Provider:    "default",
		Model:       "anthropic/claude-sonnet-4-20250514",
		MaxTokens:   4096,
		Temperature: 0.2,
	}
}

func defaultBaseURLForProvider(provider string) string {
	switch provider {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	default:
		return "https://openrouter.ai/api/v1"
	}
}
