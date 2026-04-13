package llm

import "context"

// Provider is the interface for LLM backends.
// Any OpenAI-compatible API works (OpenRouter, Anthropic, Groq, Ollama, etc.).
type Provider interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
	Name() string
}

// Tool describes a function the LLM can call.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction describes the function signature.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCall is an LLM request to invoke a tool.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type CompletionRequest struct {
	Model       string
	System      string
	Messages    []Message
	Temperature float64
	MaxTokens   int
	Tools       []Tool `json:"tools,omitempty"`
	JSONMode    bool   // When true, sends response_format: {"type": "json_object"}
}

type Message struct {
	Role       string     // "user", "assistant", "tool"
	Content    string
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type CompletionResponse struct {
	Content      string
	TokensUsed   TokenUsage
	Cost         float64
	ToolCalls    []ToolCall
	FinishReason string
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// modelPricing holds per-1M-token pricing for known models.
// Used to estimate cost when the API doesn't return it (Azure, direct OpenAI, etc.).
type modelPricing struct {
	Input  float64 // $ per 1M input tokens
	Output float64 // $ per 1M output tokens
}

var knownPricing = map[string]modelPricing{
	// OpenAI
	"gpt-4o":            {2.50, 10.00},
	"gpt-4o-mini":       {0.15, 0.60},
	"gpt-4.1":           {2.00, 8.00},
	"gpt-4.1-mini":      {0.40, 1.60},
	"gpt-4.1-nano":      {0.10, 0.40},
	"gpt-5.4":           {2.00, 8.00},
	"o3":                {2.00, 8.00},
	"o3-mini":           {1.10, 4.40},
	"o4-mini":           {1.10, 4.40},
	// Anthropic
	"claude-sonnet-4-5-20241022": {3.00, 15.00},
	"claude-3-5-haiku-20241022":  {0.80, 4.00},
	"claude-opus-4-5-20250219":   {15.00, 75.00},
	// DeepSeek
	"deepseek-chat":     {0.14, 0.28},
	"deepseek-reasoner": {0.55, 2.19},
	// Fireworks
	"accounts/fireworks/models/glm-5p1": {0.10, 0.10},
}

// EstimateCost calculates cost from token counts when the API doesn't return it.
// Returns 0 if the model isn't in the pricing table.
func EstimateCost(model string, usage TokenUsage) float64 {
	p, ok := knownPricing[model]
	if !ok {
		// Try prefix match for versioned models (gpt-5.4-2026-03-05 → gpt-5.4)
		for prefix, pricing := range knownPricing {
			if len(model) > len(prefix) && model[:len(prefix)] == prefix && (model[len(prefix)] == '-' || model[len(prefix)] == '.') {
				p = pricing
				ok = true
				break
			}
		}
		if !ok {
			return 0
		}
	}
	return (float64(usage.PromptTokens) * p.Input / 1_000_000) +
		(float64(usage.CompletionTokens) * p.Output / 1_000_000)
}
