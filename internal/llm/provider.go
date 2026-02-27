package llm

import "context"

// Provider is the interface for LLM backends.
// Any OpenAI-compatible API works (OpenRouter, Anthropic, Groq, Ollama, etc.).
type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
	Name() string
}

type CompletionRequest struct {
	Model       string
	System      string
	Messages    []Message
	Temperature float64
	MaxTokens   int
}

type Message struct {
	Role    string // "user", "assistant"
	Content string
}

type CompletionResponse struct {
	Content    string
	TokensUsed TokenUsage
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}
