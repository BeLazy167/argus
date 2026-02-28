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
	ToolCalls    []ToolCall
	FinishReason string
}

type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}
