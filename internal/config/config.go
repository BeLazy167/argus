package config

import (
	"fmt"
	"os"
	"strconv"
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

	// LLM (OpenAI-compatible)
	LLMAPIKey          string
	LLMBaseURL         string
	DefaultReviewModel string
	DefaultTriageModel string

	// Encryption
	EncryptionKey string

	// Supermemory
	SupermemoryAPIKey string

	// Worker
	MaxConcurrentReviews int
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
	maxWorkers, err := strconv.Atoi(getEnv("MAX_CONCURRENT_REVIEWS", "5"))
	if err != nil {
		return nil, fmt.Errorf("invalid MAX_CONCURRENT_REVIEWS: %w", err)
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

	cfg := &Config{
		Port: port,
		Env:  getEnv("ENV", "development"),

		DatabaseURL: dbURL,

		GitHubAppID:         appID,
		GitHubPrivateKey:    privateKey,
		GitHubWebhookSecret: webhookSecret,

		ClerkJWKSURL:    os.Getenv("CLERK_JWKS_URL"),
		CORSAllowOrigin: getEnv("CORS_ALLOW_ORIGIN", "http://localhost:3000"),

		LLMAPIKey:          os.Getenv("LLM_API_KEY"),
		LLMBaseURL:         getEnv("LLM_BASE_URL", "https://openrouter.ai/api/v1"),
		DefaultReviewModel: getEnv("DEFAULT_REVIEW_MODEL", "anthropic/claude-sonnet-4-20250514"),
		DefaultTriageModel: getEnv("DEFAULT_TRIAGE_MODEL", "openai/gpt-4o-mini"),

		EncryptionKey: os.Getenv("ENCRYPTION_KEY"),

		SupermemoryAPIKey: os.Getenv("SUPERMEMORY_API_KEY"),

		MaxConcurrentReviews: maxWorkers,
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
