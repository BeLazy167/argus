package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

// AuthStyle controls how the API key is sent in HTTP requests.
type AuthStyle int

const (
	AuthBearer AuthStyle = iota // Authorization: Bearer <key> (OpenAI, OpenRouter, Groq, etc.)
	AuthAPIKey                  // api-key: <key> (Azure OpenAI)
	AuthAPIM                    // Ocp-Apim-Subscription-Key: <key> (Azure API Management)
)

// ChatProvider implements the Provider interface using the OpenAI-compatible
// chat completions format. Works with OpenRouter, OpenAI, Azure, AWS Bedrock,
// GCP Vertex, Anthropic, Groq, Together, xAI/Grok, DeepSeek, Ollama, vLLM, etc.
type ChatProvider struct {
	name            string
	apiKey          string
	baseURL         string
	referer         string // HTTP-Referer for OpenRouter attribution; set by Registry
	authStyle       AuthStyle
	pathFn          func(model string) string // custom path builder (nil = default /chat/completions)
	useResponsesAPI bool                      // Azure Foundry Responses API format
	client          *http.Client
}

// llmClientTimeout bounds a single HTTP request to any provider. Must exceed
// Azure gpt-5.4's observed worst-case latency (TTFT ~215s + generation at
// ~56 tok/s for MaxTokens 8000 ≈ 360s) with headroom. 600s is conservative
// for fast providers (Groq/Together typically <10s) but essential for
// reasoning-heavy Azure calls — the prior 120s ceiling was below gpt-5.4's
// first-token latency, causing the exact failure this package was changed
// to prevent. Operator can still cancel mid-flight via the run's bound
// cancel (inflight.Slot.BindCancel, wired by pipeline.Launcher).
const llmClientTimeout = 600 * time.Second

func NewChatProvider(name, apiKey, baseURL string) *ChatProvider {
	return &ChatProvider{
		name:    name,
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: llmClientTimeout},
	}
}

// NewAzureProvider creates a provider for Azure OpenAI Service.
// Supports three endpoint styles:
//   - Classic: https://{resource}.openai.azure.com (deployment-based path)
//   - Foundry MaaS: https://{endpoint}.inference.ai.azure.com (OpenAI-compatible)
//   - Cognitive Services: https://{resource}.cognitiveservices.azure.com (Responses API, Bearer auth)
//
// Append ?api-version=YYYY-MM-DD to baseURL to override the default version.
func NewAzureProvider(apiKey, baseURL string) *ChatProvider {
	apiVersion := "2024-10-21"
	if u, err := url.Parse(baseURL); err == nil {
		if v := u.Query().Get("api-version"); v != "" {
			apiVersion = v
			q := u.Query()
			q.Del("api-version")
			u.RawQuery = q.Encode()
			baseURL = u.String()
		}
	}

	// Foundry MaaS endpoints use standard OpenAI-compatible paths
	isMaaS := strings.Contains(baseURL, ".inference.ai.azure.com") ||
		strings.Contains(baseURL, ".services.ai.azure.com")

	// Cognitive Services / AI Foundry endpoints use Responses API with Bearer auth
	isCognitive := strings.Contains(baseURL, ".cognitiveservices.azure.com")

	// Azure API Management gateway (e.g., spend limiter proxy)
	isAPIM := strings.Contains(baseURL, ".azure-api.net")

	authStyle := AuthAPIKey
	var pathFn func(string) string

	if isAPIM {
		// APIM proxy: standard deployment path, Ocp-Apim-Subscription-Key auth
		authStyle = AuthAPIM
		pathFn = func(model string) string {
			return "/openai/deployments/" + model + "/chat/completions?api-version=" + apiVersion
		}
	} else if isCognitive {
		// Azure AI Foundry (cognitiveservices): Responses API, Bearer auth, model in body
		authStyle = AuthBearer
		if apiVersion == "2024-10-21" {
			apiVersion = "2025-04-01-preview"
		}
		pathFn = func(_ string) string {
			return "/openai/responses?api-version=" + apiVersion
		}
	} else if !isMaaS {
		// Classic Azure OpenAI: deployment name in URL path
		pathFn = func(model string) string {
			return "/openai/deployments/" + model + "/chat/completions?api-version=" + apiVersion
		}
	}

	return &ChatProvider{
		name:            "azure",
		apiKey:          apiKey,
		baseURL:         baseURL,
		authStyle:       authStyle,
		pathFn:          pathFn,
		useResponsesAPI: isCognitive,
		client:          &http.Client{Timeout: llmClientTimeout},
	}
}

// NewGCPVertexProvider creates a provider for GCP Vertex AI (OpenAI-compatible endpoint).
// baseURL should be: https://{region}-aiplatform.googleapis.com/v1/projects/{project}/locations/{region}/endpoints/openapi
// API key should be a GCP access token from `gcloud auth print-access-token`.
func NewGCPVertexProvider(apiKey, baseURL string) *ChatProvider {
	return &ChatProvider{
		name:    "gcp_vertex",
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: llmClientTimeout},
	}
}

// NewAWSBedrockProvider creates a provider for AWS Bedrock (OpenAI-compatible endpoint).
// baseURL should be: https://bedrock-runtime.{region}.amazonaws.com
func NewAWSBedrockProvider(apiKey, baseURL string) *ChatProvider {
	return &ChatProvider{
		name:    "aws_bedrock",
		apiKey:  apiKey,
		baseURL: baseURL,
		pathFn: func(_ string) string {
			return "/openai/v1/chat/completions"
		},
		client: &http.Client{Timeout: llmClientTimeout},
	}
}

func (p *ChatProvider) Name() string { return p.name }

// Complete wraps the underlying HTTP call with structured telemetry. Every
// invocation emits exactly one slog record — `llm.call.completed` on success
// (Info), `llm.call.failed` on error (Error) — both carrying stage, model,
// provider, and a numeric status_code so PostHog can slice cost/latency by
// stage without crossing the log-stream boundary. Forwarding is driven by
// the `event=` attr, so adding new record attrs does not require new
// handler logic; it does require the attr key to appear in obs.AllowedKeys.
func (p *ChatProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	start := time.Now()
	resp, statusCode, err := p.complete(ctx, req)
	durationMs := time.Since(start).Milliseconds()

	if err != nil {
		slog.ErrorContext(ctx, "llm call failed",
			slog.String("event", "llm.call.failed"),
			slog.String("provider", p.name),
			slog.String("model", req.Model),
			slog.String("stage", req.Stage),
			slog.String("error_class", classifyLLMError(err)),
			slog.Int("status_code", statusCode),
			slog.Int64("duration_ms", durationMs),
			slog.String("trace_id", obs.TraceID(ctx)),
		)
		return resp, err
	}

	slog.InfoContext(ctx, "llm call completed",
		slog.String("event", "llm.call.completed"),
		slog.String("provider", p.name),
		slog.String("model", req.Model),
		slog.String("stage", req.Stage),
		slog.Int("prompt_tokens", resp.TokensUsed.PromptTokens),
		slog.Int("completion_tokens", resp.TokensUsed.CompletionTokens),
		slog.Float64("cost_usd", resp.Cost),
		slog.Int64("duration_ms", durationMs),
		slog.String("trace_id", obs.TraceID(ctx)),
	)
	return resp, nil
}

// complete is the untyped inner implementation of Complete. Split out solely
// so the public method can emit telemetry without every return path needing
// duplicated defer/log code. statusCode is 0 for non-HTTP errors (marshal
// failures, transport errors before a response was received) and the
// upstream status code otherwise — feeds directly into the `status_code`
// telemetry attr.
func (p *ChatProvider) complete(ctx context.Context, req CompletionRequest) (CompletionResponse, int, error) {
	// Reject unrecognized ReasoningEffort values before the HTTP round-trip.
	// Azure rejects the same garbage with a 400, but mid-pipeline that surfaces
	// as a specialist failure with an opaque provider error — fail fast here
	// so callers get a typed error at the call site instead.
	if !req.ReasoningEffort.Valid() {
		return CompletionResponse{}, 0, fmt.Errorf("invalid reasoning_effort %q: must be one of minimal|low|medium|high|xhigh (or empty for provider default)", req.ReasoningEffort)
	}
	msgs := make([]chatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, chatMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
		})
	}

	body := chatRequest{
		Model:    req.Model,
		Messages: msgs,
		Tools:    req.Tools,
	}
	if req.JSONMode {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}

	// Set standard params, then let provider-specific adjustments override
	if p.useResponsesAPI {
		body.MaxCompletionTokens = req.MaxTokens
	} else {
		body.MaxTokens = req.MaxTokens
	}
	body.Temperature = &req.Temperature
	// Caller-provided ReasoningEffort flows through to both possible shapes
	// (top-level for Azure, wrapped for OpenRouter); adjustRequestForProvider
	// picks the right one and sets a gpt-5.x default when unset. The cast to
	// string happens here because chatRequest is the wire shape — Azure/
	// OpenRouter don't know about our typed constants.
	if req.ReasoningEffort != ReasoningNone {
		body.ReasoningEffort = string(req.ReasoningEffort)
	}
	p.adjustRequestForProvider(&body, req.Model)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return CompletionResponse{}, 0, fmt.Errorf("marshaling request: %w", err)
	}

	path := "/chat/completions"
	if p.pathFn != nil {
		path = p.pathFn(req.Model)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+path, bytes.NewReader(jsonBody))
	if err != nil {
		return CompletionResponse{}, 0, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	switch p.authStyle {
	case AuthAPIKey:
		httpReq.Header.Set("api-key", p.apiKey)
	case AuthAPIM:
		httpReq.Header.Set("Ocp-Apim-Subscription-Key", p.apiKey)
	default:
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	if p.isOpenRouter() {
		if p.referer != "" {
			httpReq.Header.Set("HTTP-Referer", p.referer)
		}
		httpReq.Header.Set("X-Title", "Argus")
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, 0, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return CompletionResponse{}, resp.StatusCode, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, resp.StatusCode, fmt.Errorf("LLM API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	// Azure Responses API has a different response structure
	if p.useResponsesAPI {
		cr, err := p.parseResponsesAPI(respBody, req.Model)
		return cr, resp.StatusCode, err
	}

	var result chatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return CompletionResponse{}, resp.StatusCode, fmt.Errorf("unmarshaling response: %w", err)
	}

	if len(result.Choices) == 0 {
		return CompletionResponse{}, resp.StatusCode, fmt.Errorf("no choices in response")
	}

	usage := TokenUsage{
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
	}
	cost := result.Usage.Cost
	if cost == 0 {
		cost = EstimateCost(req.Model, usage)
	}
	return CompletionResponse{
		Content:      cleanResponseContent(result.Choices[0].Message.Content),
		TokensUsed:   usage,
		Cost:         cost,
		ToolCalls:    result.Choices[0].Message.ToolCalls,
		FinishReason: result.Choices[0].FinishReason,
	}, resp.StatusCode, nil
}

// classifyLLMError buckets errors into a short vocabulary consumable by
// PostHog funnels. Bucket assignment is status-code first (HTTP 429 / 401 /
// 4xx / 5xx) with context-cancellation fallbacks at the top. Keeping the
// vocabulary small is deliberate — a PostHog breakdown splits by every
// unique value, so `server_error` aggregates far better than a raw `502`.
// Callers supply the statusCode from the HTTP round-trip separately when
// they want the raw number.
func classifyLLMError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	// The chatProvider wraps every non-2xx response as "LLM API error (status N)".
	// Parse the status out of the error message so we can bucket without a
	// typed error shape — refactoring to one would churn every call site.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "status 429"):
		return "rate_limit"
	case strings.Contains(msg, "status 401") || strings.Contains(msg, "status 403"):
		return "unauthorized"
	case strings.Contains(msg, "status 400") || strings.Contains(msg, "status 404") || strings.Contains(msg, "status 422"):
		return "bad_request"
	case strings.Contains(msg, "status 5"):
		return "server_error"
	}
	return "other"
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type chatRequest struct {
	Model               string        `json:"model"`
	Messages            []chatMessage `json:"messages"`
	MaxTokens           int           `json:"max_tokens,omitempty"`
	MaxCompletionTokens int           `json:"max_completion_tokens,omitempty"`
	Temperature         *float64      `json:"temperature,omitempty"`
	Tools               []Tool        `json:"tools,omitempty"`
	// Reasoning is OpenRouter's wrapped form — {"reasoning":{"effort":"..."}}.
	Reasoning *reasoningConfig `json:"reasoning,omitempty"`
	// ReasoningEffort is Azure/OpenAI's top-level form for gpt-5.x reasoning
	// models. Must not overlap with Reasoning: adjustRequestForProvider picks
	// the right shape per provider.
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"` // "json_object" or "text"
}

type reasoningConfig struct {
	Effort  string `json:"effort,omitempty"`
	Exclude bool   `json:"exclude,omitempty"`
}

// adjustRequestForProvider modifies the chat request based on provider and model
// capabilities. Each provider has different rules for reasoning models.
//
// Two reasoning shapes exist on chatRequest:
//
//   - body.Reasoning (wrapped {reasoning: {effort, exclude}}) — OpenRouter format.
//   - body.ReasoningEffort (top-level reasoning_effort) — Azure/OpenAI format.
//
// Callers set only `CompletionRequest.ReasoningEffort` and we route it to the
// correct wire shape per provider here. Both fields must never be populated on
// the same marshalled body — at the end we enforce that invariant explicitly.
func (p *ChatProvider) adjustRequestForProvider(body *chatRequest, model string) {
	m := strings.ToLower(model)

	// Layer 1: OpenRouter — always exclude reasoning from content field.
	// This prevents thinking tokens from leaking into the response for ALL models.
	if p.isOpenRouter() {
		body.Reasoning = &reasoningConfig{Exclude: true}
	}

	switch {
	case p.isOpenRouter() && isOpenAIReasoning(m):
		// OpenRouter + o-series: route caller's effort to wrapped form.
		// Default "medium" preserved for callers that didn't set one — matches
		// pre-ReasoningEffort-field behavior so existing code doesn't regress.
		if body.ReasoningEffort != "" {
			body.Reasoning.Effort = body.ReasoningEffort
		} else {
			body.Reasoning.Effort = "medium"
		}

	case isOpenAIReasoning(m):
		// Direct OpenAI / Azure: o-series needs max_completion_tokens, no temperature
		body.MaxCompletionTokens = body.MaxTokens
		body.MaxTokens = 0
		body.Temperature = nil

	case requiresMaxCompletionTokens(m):
		// GPT-5+ require max_completion_tokens instead of max_tokens.
		body.MaxCompletionTokens = body.MaxTokens
		body.MaxTokens = 0
		// gpt-5.x is a reasoning model and rejects any `temperature` other than
		// the default 1. Observed production failure:
		//   "Unsupported value: 'temperature' does not support 0.2 with this
		//    model. Only the default (1) value is supported." (HTTP 400)
		// Mirror the o-series branch above: strip the temperature so Azure
		// applies its default. Callers still set req.Temperature for non-
		// reasoning models, so the field stays in CompletionRequest.
		body.Temperature = nil
		// gpt-5.x is a reasoning model. Azure's implicit default reasoning
		// level silently consumes the entire max_completion_tokens budget
		// before emitting any visible output — the observed failure mode on
		// a production review was `completion_tokens=5, response="[]"` across
		// every specialist. Force "minimal" unless the caller explicitly set
		// an effort via req.ReasoningEffort. Callers that want deeper
		// reasoning (specialists, synthesis) pass "low" or "medium".
		if body.ReasoningEffort == "" {
			body.ReasoningEffort = "minimal"
		}
		// On OpenRouter-routed gpt-5.x, route the effort to the wrapped form
		// instead of the top-level field — OpenRouter doesn't accept the
		// top-level `reasoning_effort` and forwards the wrapped one.
		if p.isOpenRouter() {
			body.Reasoning.Effort = body.ReasoningEffort
			body.ReasoningEffort = ""
		}

	case p.name == "anthropic" && isAnthropicThinking(m):
		// Anthropic extended thinking requires temperature=1.0
		temp := 1.0
		body.Temperature = &temp
	}

	// Invariant: never ship both wrapped AND top-level reasoning on the same
	// body. OpenRouter gets wrapped; every other provider gets top-level.
	// A trailing belt-and-suspenders clean-up in case a new branch sets both.
	if p.isOpenRouter() {
		body.ReasoningEffort = ""
	} else if body.ReasoningEffort != "" {
		body.Reasoning = nil
	}
}

func (p *ChatProvider) isOpenRouter() bool {
	return p.name == "openrouter" || strings.Contains(p.baseURL, "openrouter.ai")
}

func isOpenAIReasoning(m string) bool {
	for _, prefix := range []string{"o1", "o3", "o4"} {
		if m == prefix ||
			strings.HasPrefix(m, prefix+"-") ||
			strings.HasPrefix(m, prefix+".") ||
			strings.Contains(m, "/"+prefix+"-") ||
			strings.Contains(m, "/"+prefix+".") ||
			strings.HasSuffix(m, "/"+prefix) {
			return true
		}
	}
	return false
}

// requiresMaxCompletionTokens returns true for models that reject max_tokens
// and require max_completion_tokens instead (GPT-5+).
func requiresMaxCompletionTokens(m string) bool {
	return strings.HasPrefix(m, "gpt-5") || strings.Contains(m, "/gpt-5")
}

func isAnthropicThinking(m string) bool {
	return strings.Contains(m, "thinking") || strings.Contains(m, "extended")
}

// parseResponsesAPI handles Azure AI Foundry Responses API format.
// Response: {"output": [{"type":"message","content":[{"type":"output_text","text":"..."}]}], "usage":{...}}
func (p *ChatProvider) parseResponsesAPI(body []byte, model string) (CompletionResponse, error) {
	var result struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			// Fallback: some responses use "message" format
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"output"`
		// Chat completions compat: try choices too
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
			// Chat completions compat
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("unmarshaling responses API: %w", err)
	}

	var content string
	// Try Responses API output format
	for _, out := range result.Output {
		for _, c := range out.Content {
			if c.Type == "output_text" && c.Text != "" {
				content = c.Text
				break
			}
		}
		if content == "" && out.Message.Content != "" {
			content = out.Message.Content
		}
		if content != "" {
			break
		}
	}
	// Fallback to chat completions choices
	if content == "" && len(result.Choices) > 0 {
		content = result.Choices[0].Message.Content
	}
	if content == "" {
		return CompletionResponse{}, fmt.Errorf("no content in responses API output: %s", string(body[:min(len(body), 200)]))
	}

	prompt := result.Usage.InputTokens
	if prompt == 0 {
		prompt = result.Usage.PromptTokens
	}
	completion := result.Usage.OutputTokens
	if completion == 0 {
		completion = result.Usage.CompletionTokens
	}
	total := result.Usage.TotalTokens
	if total == 0 {
		total = prompt + completion
	}

	usage := TokenUsage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
	}
	return CompletionResponse{
		Content:    cleanResponseContent(content),
		TokensUsed: usage,
		Cost:       EstimateCost(model, usage),
	}, nil
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int     `json:"prompt_tokens"`
		CompletionTokens int     `json:"completion_tokens"`
		TotalTokens      int     `json:"total_tokens"`
		Cost             float64 `json:"cost"`
	} `json:"usage"`
}

// cleanResponseContent strips reasoning model chain-of-thought from response content.
// Handles: <think> tags (DeepSeek R1, Kimi K2, Qwen QwQ), <reasoning>/<thought> variants,
// and untagged reasoning prefixes (Fireworks-routed models that dump "The user wants..." text).
// Applied to every LLM response in Complete() so all downstream callers get clean content.
func cleanResponseContent(content string) string {
	if content == "" {
		return content
	}

	// Layer 2: Strip <think>...</think> and variant tags
	for _, tag := range []string{"think", "reasoning", "thought"} {
		open := "<" + tag + ">"
		close := "</" + tag + ">"
		for {
			start := strings.Index(content, open)
			if start == -1 {
				break
			}
			end := strings.Index(content[start:], close)
			if end == -1 {
				// Unclosed tag — strip from open tag to end of content
				content = content[:start]
				break
			}
			content = content[:start] + content[start+end+len(close):]
		}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return content
	}

	// Layer 3+4: If content doesn't start with expected output (JSON, markdown),
	// find the first structural marker and discard reasoning prefix
	first := content[0]
	if first == '[' || first == '{' || first == '#' || first == '|' ||
		strings.HasPrefix(content, "**") || strings.HasPrefix(content, "```") {
		return content // Already clean
	}

	// Content starts with reasoning text — find actual output
	markers := []string{"[{", "[\"", "{\"", "**Verdict:**", "**", "```", "# "}
	bestIdx := -1
	for _, m := range markers {
		idx := strings.Index(content, m)
		if idx > 0 && (bestIdx == -1 || idx < bestIdx) {
			bestIdx = idx
		}
	}
	if bestIdx > 0 {
		content = strings.TrimSpace(content[bestIdx:])
	}

	return content
}
