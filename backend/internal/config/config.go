package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	Port int
	Env  string // "development", "production"

	// Database
	DatabaseURL string

	// GitHub App
	GitHubAppID         int64
	GitHubPrivateKey    []byte
	GitHubWebhookSecret string

	// Clerk (auth)
	ClerkJWKSURL    string
	CORSAllowOrigin string

	// Encryption
	EncryptionKey string

	// Embeddings (memory-in-Postgres program). The platform key makes memory
	// work for every managed-tier installation; BYOK "embeddings" provider
	// keys override per installation. Self-hosters may set only the base URL
	// for a keyless local endpoint (Ollama/TEI).
	//
	// Default model: voyage-4 (native 1024 dims — the memories storage dim).
	// OpenAI text-embedding-3-* BYOK models Matryoshka-truncate to
	// EmbeddingsDimensions via their `dimensions` param; custom endpoints must
	// serve 1024-dim models.
	EmbeddingsAPIKey     string
	EmbeddingsBaseURL    string
	EmbeddingsModel      string
	EmbeddingsDimensions int

	// TypeSafe (Jev) classifier key. When set, five surfaces consult Jev:
	// addressed judge, convention relations, intent verification, scoring FP
	// pre-filter, and an observe-only triage shadow. Confident answers skip the
	// LLM; the uncertain band escalates to the configured models as before.
	// Empty disables every Jev call. Egress also requires each installation's
	// jev_classifier feature flag (default OFF): the key alone never sends
	// finding text, PR metadata/body, intent, diff hunks, or stored
	// conventions to api.typesafe.ai.
	TypeSafeAPIKey string

	// OpenRouterPricingEnabled gates the model_pricing miss fallback to
	// OpenRouter's public model catalog. The fetch is a bare GET for a public
	// list — no keys, no tenant content — but installs with restricted egress
	// can disable it; lookups then resolve manual rows only.
	OpenRouterPricingEnabled bool

	// Worker
	MaxConcurrentReviews int

	// Deployment identity (self-hosting)
	DashboardBaseURL        string // web dashboard base URL, linked from GitHub comments
	MermaidValidatorBaseURL string // explicit backend→dashboard parser origin; no vendor default
	MermaidValidatorSecret  string // shared backend→dashboard validator credential
	APIBaseURL              string // public API base URL, used for signed export links
	GitHubAppSlug           string // GitHub App slug, used to build install URLs
	SelfHosted              bool   // self-hosted deployment; affects auto-run defaults and install listing
}

// MermaidValidatorEnabled reports whether diagram generation has its explicit,
// deployment-local parser endpoint and credential. Load guarantees the pair is
// either fully configured or intentionally disabled.
func (c *Config) MermaidValidatorEnabled() bool {
	return c != nil && c.MermaidValidatorBaseURL != "" && c.MermaidValidatorSecret != ""
}

func Load() (*Config, error) {
	port, err := strconv.Atoi(getEnv("PORT", "8080"))
	if err != nil {
		return nil, fmt.Errorf("invalid PORT: %w", err)
	}
	appID, err := strconv.ParseInt(getEnv("GITHUB_APP_ID", "0"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid GITHUB_APP_ID: %w", err)
	}
	maxWorkers, err := strconv.Atoi(getEnv("MAX_CONCURRENT_REVIEWS", "10"))
	if err != nil {
		return nil, fmt.Errorf("invalid MAX_CONCURRENT_REVIEWS: %w", err)
	}
	embedDims, err := strconv.Atoi(getEnv("EMBEDDINGS_DIMENSIONS", "1024"))
	if err != nil {
		return nil, fmt.Errorf("invalid EMBEDDINGS_DIMENSIONS: %w", err)
	}

	privateKey, err := loadPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("loading github private key: %w", err)
	}

	dbURL, err := requireEnv("DATABASE_URL")
	if err != nil {
		return nil, err
	}
	webhookSecret, err := requireEnv("GITHUB_WEBHOOK_SECRET")
	if err != nil {
		return nil, err
	}
	validatorBaseURL := os.Getenv("MERMAID_VALIDATOR_BASE_URL")
	validatorSecret := os.Getenv("MERMAID_VALIDATOR_SECRET")
	if (validatorBaseURL == "") != (validatorSecret == "") {
		return nil, fmt.Errorf("MERMAID_VALIDATOR_BASE_URL and MERMAID_VALIDATOR_SECRET must be configured together")
	}
	if validatorBaseURL != "" {
		parsed, parseErr := url.Parse(validatorBaseURL)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("MERMAID_VALIDATOR_BASE_URL must be an absolute http(s) URL")
		}
	}

	cfg := &Config{
		Port: port,
		Env:  getEnv("ENV", "development"),

		DatabaseURL: dbURL,

		GitHubAppID:         appID,
		GitHubPrivateKey:    privateKey,
		GitHubWebhookSecret: webhookSecret,

		ClerkJWKSURL:    os.Getenv("CLERK_JWKS_URL"),
		CORSAllowOrigin: getEnv("CORS_ALLOW_ORIGIN", "http://localhost:3000"),

		EncryptionKey: os.Getenv("ENCRYPTION_KEY"),

		// The TypeSafe SDK spells the var TYPESAFE_AI_API_KEY; accept both so
		// an operator copying either doc gets a working config. Trim before
		// the fallback check — a whitespace-only primary must not shadow a
		// valid TYPESAFE_AI_API_KEY.
		TypeSafeAPIKey: firstNonBlankEnv("TYPESAFE_API_KEY", "TYPESAFE_AI_API_KEY"),

		EmbeddingsAPIKey:     os.Getenv("EMBEDDINGS_API_KEY"),
		EmbeddingsBaseURL:    getEnv("EMBEDDINGS_BASE_URL", "https://api.voyageai.com/v1"),
		EmbeddingsModel:      getEnv("EMBEDDINGS_MODEL", "voyage-4"),
		EmbeddingsDimensions: embedDims,

		// Default ON: the catalog GET carries no tenant data or keys — it is
		// a public model list. Restricted-egress installs set it to "false".
		OpenRouterPricingEnabled: getEnv("OPENROUTER_PRICING_ENABLED", "true") == "true",

		MaxConcurrentReviews: maxWorkers,

		DashboardBaseURL:        getEnv("DASHBOARD_BASE_URL", "https://argus.reviews"),
		MermaidValidatorBaseURL: validatorBaseURL,
		MermaidValidatorSecret:  validatorSecret,
		APIBaseURL:              getEnv("API_BASE_URL", "https://api.argus.reviews"),
		GitHubAppSlug:           getEnv("GITHUB_APP_SLUG", "argus-eye"),
		SelfHosted:              getEnv("SELF_HOSTED", "false") == "true",
	}

	return cfg, nil
}

func loadPrivateKey() ([]byte, error) {
	path := os.Getenv("GITHUB_PRIVATE_KEY_PATH")
	if path != "" {
		return os.ReadFile(path)
	}
	key := os.Getenv("GITHUB_PRIVATE_KEY")
	if key != "" {
		return []byte(key), nil
	}
	return nil, fmt.Errorf("set GITHUB_PRIVATE_KEY_PATH or GITHUB_PRIVATE_KEY")
}

// firstNonBlankEnv returns the first env var whose trimmed value is
// non-empty — a whitespace-only value must not shadow later candidates.
func firstNonBlankEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("required env var %s is not set", key)
	}
	return v, nil
}
