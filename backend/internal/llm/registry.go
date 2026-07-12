package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Configuration-gap sentinels: callers use errors.Is to distinguish "the user
// never configured this" (guided-setup surfaces) from transient resolution
// failures (DB/network), which must never trigger setup nudges.
var (
	// ErrNoModelConfig — neither a repo- nor an org-level model_configs row
	// exists for the requested stage.
	ErrNoModelConfig = errors.New("no model config")
	// ErrNoAPIKey — no BYOK provider key stored for the installation.
	ErrNoAPIKey = errors.New("no API key")
)

// PipelineStage identifies which stage of the review pipeline a model config applies to.
type PipelineStage string

const (
	StageTriage    PipelineStage = "triage"
	StageReview    PipelineStage = "review"
	StageScoring   PipelineStage = "scoring"
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

// ModelConfigLister returns pre-converted llm.ModelConfig entries for a repo.
type ModelConfigLister interface {
	ListLLMConfigs(ctx context.Context, repoID int64) ([]ModelConfig, error)
}

type cachedProvider struct {
	provider  Provider
	expiresAt time.Time
}

// Registry manages LLM providers via BYOK keys resolved from the database.
type Registry struct {
	resolver      KeyResolver
	cacheMu       sync.RWMutex
	providerCache map[string]cachedProvider
	cacheTTL      time.Duration
}

func NewRegistry() *Registry {
	return &Registry{
		providerCache: make(map[string]cachedProvider),
		cacheTTL:      5 * time.Minute,
	}
}

// SetResolver configures the DB-backed key resolver for dynamic provider creation.
func (r *Registry) SetResolver(resolver KeyResolver) {
	r.resolver = resolver
}

// GetProviderForRepo resolves a provider from BYOK keys in the database.
func (r *Registry) GetProviderForRepo(ctx context.Context, installationID int64, repoID *int64, providerName string) (Provider, error) {
	if r.resolver == nil {
		return nil, fmt.Errorf("no key resolver configured")
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

	if !found {
		return nil, fmt.Errorf("%w for provider %q — add one at the dashboard settings page", ErrNoAPIKey, providerName)
	}

	if baseURL == "" {
		baseURL = defaultBaseURLForProvider(providerName)
	}
	p := newProviderForName(providerName, apiKey, baseURL)
	r.cacheMu.Lock()
	r.providerCache[cacheKey] = cachedProvider{provider: p, expiresAt: time.Now().Add(r.cacheTTL)}
	r.cacheMu.Unlock()
	return p, nil
}

// GetConfig returns the model config for a repo + stage.
// Prefers repo-specific config; falls back to org-level (RepoID == 0) if
// present. There is deliberately NO platform default tier: org defaults ARE
// the user-selected defaults, so an unconfigured stage errors and surfaces as
// an explicit, guided setup step instead of silently running a model the user
// never chose.
func (r *Registry) GetConfig(repoID int64, stage PipelineStage, repoConfigs []ModelConfig) (ModelConfig, error) {
	var orgFallback *ModelConfig
	for _, cfg := range repoConfigs {
		if cfg.Stage == stage {
			if cfg.RepoID == repoID {
				return cfg, nil
			}
			if cfg.RepoID == 0 {
				c := cfg
				orgFallback = &c
			}
		}
	}
	if orgFallback != nil {
		return *orgFallback, nil
	}
	return ModelConfig{}, fmt.Errorf("%w for repo %d stage %s — configure one in the dashboard", ErrNoModelConfig, repoID, stage)
}

// ResolveProvider resolves the LLM provider and model config for a pipeline stage.
func (r *Registry) ResolveProvider(ctx context.Context, cfgLister ModelConfigLister, installationID, repoID int64, stage PipelineStage) (Provider, ModelConfig, error) {
	configs, err := cfgLister.ListLLMConfigs(ctx, repoID)
	if err != nil {
		return nil, ModelConfig{}, fmt.Errorf("resolve %s: list configs: %w", stage, err)
	}
	cfg, err := r.GetConfig(repoID, stage, configs)
	if err != nil {
		return nil, ModelConfig{}, fmt.Errorf("resolve %s: %w", stage, err)
	}
	provider, err := r.GetProviderForRepo(ctx, installationID, &repoID, cfg.Provider)
	if err != nil {
		return nil, ModelConfig{}, fmt.Errorf("resolve %s provider: %w", stage, err)
	}
	return provider, cfg, nil
}

// HasKeyForRepo returns true if a BYOK key exists in the database for this provider.
func (r *Registry) HasKeyForRepo(ctx context.Context, installationID int64, repoID *int64, providerName string) bool {
	if r.resolver == nil {
		return false
	}
	_, _, found, err := r.resolver.ResolveAPIKey(ctx, installationID, repoID, providerName)
	return err == nil && found
}

// newProviderForName creates the appropriate provider based on the provider name.
// Azure, GCP, and AWS use specialized constructors; everything else uses the generic ChatProvider.
func newProviderForName(name, apiKey, baseURL string) *ChatProvider {
	switch name {
	case "azure":
		return NewAzureProvider(apiKey, baseURL)
	case "gcp_vertex":
		return NewGCPVertexProvider(apiKey, baseURL)
	case "aws_bedrock":
		return NewAWSBedrockProvider(apiKey, baseURL)
	default:
		return NewChatProvider(name, apiKey, baseURL)
	}
}

func defaultBaseURLForProvider(provider string) string {
	switch provider {
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "zhipu":
		return "https://api.z.ai/api/paas/v4"
	case "fireworks":
		return "https://api.fireworks.ai/inference/v1"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "together":
		return "https://api.together.xyz/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "azure":
		return "" // Azure requires user-provided endpoint (https://{resource}.openai.azure.com/openai)
	case "gcp_vertex":
		return "" // GCP requires user-provided endpoint
	case "aws_bedrock":
		return "" // AWS requires user-provided endpoint
	default:
		return "https://openrouter.ai/api/v1"
	}
}
