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

// ReasoningEffort controls chain-of-thought depth for reasoning models
// (gpt-5.x, o-series). The empty value means "provider default" — the adapter
// in chat.go forces "minimal" for gpt-5.x in that case, because Azure's
// default reasoning level silently consumes the entire max_completion_tokens
// budget before emitting visible output. Callers that want deeper reasoning
// (specialists, synthesis) must set this explicitly.
//
// Callers should use the defined constants (ReasoningLow, ReasoningMedium,
// etc.) rather than raw strings — chat.go.Complete rejects an unrecognized
// value with a typed error before the HTTP call fires.
type ReasoningEffort string

const (
	// ReasoningNone means "no explicit effort set"; the adapter applies its
	// per-provider default (e.g. "minimal" for gpt-5.x direct-to-Azure).
	ReasoningNone    ReasoningEffort = ""
	ReasoningMinimal ReasoningEffort = "minimal"
	ReasoningLow     ReasoningEffort = "low"
	ReasoningMedium  ReasoningEffort = "medium"
	ReasoningHigh    ReasoningEffort = "high"
	// ReasoningXHigh (Azure terminology) == extended reasoning; slowest and
	// most expensive. TTFT can exceed 200s on gpt-5.4. Use sparingly.
	ReasoningXHigh ReasoningEffort = "xhigh"
)

// Valid reports whether r is one of the recognized reasoning levels. Empty
// string (ReasoningNone) counts as valid — the adapter interprets it as
// "use provider default".
func (r ReasoningEffort) Valid() bool {
	switch r {
	case ReasoningNone, ReasoningMinimal, ReasoningLow,
		ReasoningMedium, ReasoningHigh, ReasoningXHigh:
		return true
	default:
		return false
	}
}

type CompletionRequest struct {
	Model       string
	System      string
	Messages    []Message
	Temperature float64
	MaxTokens   int
	Tools       []Tool `json:"tools,omitempty"`
	JSONMode    bool   // When true, sends response_format: {"type": "json_object"}
	// ReasoningEffort is typed to prevent garbage values from round-tripping
	// to Azure as HTTP 400. Complete rejects unrecognized values at entry.
	ReasoningEffort ReasoningEffort
	// Stage names the pipeline step that owns this LLM call ("triage",
	// "review", "scoring", "synthesis", "acceptance", "crosspr", ...). Used
	// purely for telemetry — propagated onto `llm.call.completed` / `failed`
	// slog events so PostHog can group cost and latency by stage without a
	// join back against the pipeline state. Never sent on the wire to the
	// provider.
	Stage string
}

type Message struct {
	Role       string // "user", "assistant", "tool"
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

// PricingLookup resolves model pricing. Returns (inputPer1M, outputPer1M, found).
type PricingLookup func(model string) (float64, float64, bool)

// pricingLookup is set at startup by the server. If nil, cost estimation is skipped.
var pricingLookup PricingLookup

// SetPricingLookup configures the global pricing resolver (called once at server init).
func SetPricingLookup(fn PricingLookup) { pricingLookup = fn }

// EstimateCost calculates cost from token counts using the DB-backed pricing table.
// Returns 0 if no pricing lookup is configured or model isn't found.
func EstimateCost(model string, usage TokenUsage) float64 {
	if pricingLookup == nil {
		return 0
	}
	input, output, ok := pricingLookup(model)
	if !ok {
		return 0
	}
	return (float64(usage.PromptTokens) * input / 1_000_000) +
		(float64(usage.CompletionTokens) * output / 1_000_000)
}
