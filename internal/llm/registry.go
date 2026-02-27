package llm

import (
	"fmt"
	"sync"
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

// Registry manages LLM providers and per-repo model configuration.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	defaults  map[PipelineStage]ModelConfig
}

func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
		defaults:  make(map[PipelineStage]ModelConfig),
	}
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
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return p, nil
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
