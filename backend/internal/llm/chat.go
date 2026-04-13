package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AuthStyle controls how the API key is sent in HTTP requests.
type AuthStyle int

const (
	AuthBearer AuthStyle = iota // Authorization: Bearer <key> (OpenAI, OpenRouter, Groq, etc.)
	AuthAPIKey                  // api-key: <key> (Azure OpenAI)
)

// ChatProvider implements the Provider interface using the OpenAI-compatible
// chat completions format. Works with OpenRouter, OpenAI, Azure, AWS Bedrock,
// GCP Vertex, Anthropic, Groq, Together, xAI/Grok, DeepSeek, Ollama, vLLM, etc.
type ChatProvider struct {
	name      string
	apiKey    string
	baseURL   string
	authStyle AuthStyle
	pathFn    func(model string) string // custom path builder (nil = default /chat/completions)
	client    *http.Client
}

func NewChatProvider(name, apiKey, baseURL string) *ChatProvider {
	return &ChatProvider{
		name:    name,
		apiKey:  apiKey,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 120 * time.Second},
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

	// Cognitive Services / AI Foundry endpoints use deployment path with Bearer auth
	isCognitive := strings.Contains(baseURL, ".cognitiveservices.azure.com")

	authStyle := AuthAPIKey
	var pathFn func(string) string

	if isCognitive {
		// Cognitive Services: deployment-based path, Bearer auth
		authStyle = AuthBearer
		if apiVersion == "2024-10-21" {
			apiVersion = "2025-04-01-preview"
		}
		pathFn = func(model string) string {
			return "/openai/deployments/" + model + "/chat/completions?api-version=" + apiVersion
		}
	} else if !isMaaS {
		// Classic Azure OpenAI: deployment name in URL path
		pathFn = func(model string) string {
			return "/openai/deployments/" + model + "/chat/completions?api-version=" + apiVersion
		}
	}

	return &ChatProvider{
		name:      "azure",
		apiKey:    apiKey,
		baseURL:   baseURL,
		authStyle: authStyle,
		pathFn:    pathFn,
		client:    &http.Client{Timeout: 120 * time.Second},
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
		client:  &http.Client{Timeout: 120 * time.Second},
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
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (p *ChatProvider) Name() string { return p.name }

func (p *ChatProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
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
	body.MaxTokens = req.MaxTokens
	body.Temperature = &req.Temperature
	p.adjustRequestForProvider(&body, req.Model)

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("marshaling request: %w", err)
	}

	path := "/chat/completions"
	if p.pathFn != nil {
		path = p.pathFn(req.Model)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+path, bytes.NewReader(jsonBody))
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("creating request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	switch p.authStyle {
	case AuthAPIKey:
		httpReq.Header.Set("api-key", p.apiKey)
	default:
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	if p.isOpenRouter() {
		httpReq.Header.Set("HTTP-Referer", "https://argus.reviews")
		httpReq.Header.Set("X-Title", "Argus")
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("LLM API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result chatResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return CompletionResponse{}, fmt.Errorf("unmarshaling response: %w", err)
	}

	if len(result.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("no choices in response")
	}

	return CompletionResponse{
		Content: cleanResponseContent(result.Choices[0].Message.Content),
		TokensUsed: TokenUsage{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
		},
		Cost:         result.Usage.Cost,
		ToolCalls:    result.Choices[0].Message.ToolCalls,
		FinishReason: result.Choices[0].FinishReason,
	}, nil
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type chatRequest struct {
	Model               string            `json:"model"`
	Messages            []chatMessage     `json:"messages"`
	MaxTokens           int               `json:"max_tokens,omitempty"`
	MaxCompletionTokens int               `json:"max_completion_tokens,omitempty"`
	Temperature         *float64          `json:"temperature,omitempty"`
	Tools               []Tool            `json:"tools,omitempty"`
	Reasoning           *reasoningConfig  `json:"reasoning,omitempty"`
	ResponseFormat      *responseFormat   `json:"response_format,omitempty"`
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
func (p *ChatProvider) adjustRequestForProvider(body *chatRequest, model string) {
	m := strings.ToLower(model)

	// Layer 1: OpenRouter — always exclude reasoning from content field.
	// This prevents thinking tokens from leaking into the response for ALL models.
	if p.isOpenRouter() {
		body.Reasoning = &reasoningConfig{Exclude: true}
	}

	switch {
	case p.isOpenRouter() && isOpenAIReasoning(m):
		// OpenRouter + o-series: also set reasoning effort
		body.Reasoning.Effort = "medium"

	case isOpenAIReasoning(m):
		// Direct OpenAI / Azure: o-series needs max_completion_tokens, no temperature
		body.MaxCompletionTokens = body.MaxTokens
		body.MaxTokens = 0
		body.Temperature = nil

	case p.name == "anthropic" && isAnthropicThinking(m):
		// Anthropic extended thinking requires temperature=1.0
		temp := 1.0
		body.Temperature = &temp
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

func isAnthropicThinking(m string) bool {
	return strings.Contains(m, "thinking") || strings.Contains(m, "extended")
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
