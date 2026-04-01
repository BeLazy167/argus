package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
// baseURL should be: https://{resource}.openai.azure.com/openai
func NewAzureProvider(apiKey, baseURL string) *ChatProvider {
	return &ChatProvider{
		name:      "azure",
		apiKey:    apiKey,
		baseURL:   baseURL,
		authStyle: AuthAPIKey,
		pathFn: func(model string) string {
			return "/deployments/" + model + "/chat/completions?api-version=2024-10-21"
		},
		client: &http.Client{Timeout: 120 * time.Second},
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
		httpReq.Header.Set("HTTP-Referer", "https://argusai.vercel.app")
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
		Content: result.Choices[0].Message.Content,
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
	Model               string           `json:"model"`
	Messages            []chatMessage    `json:"messages"`
	MaxTokens           int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens int              `json:"max_completion_tokens,omitempty"`
	Temperature         *float64         `json:"temperature,omitempty"`
	Tools               []Tool           `json:"tools,omitempty"`
	Reasoning           *reasoningConfig `json:"reasoning,omitempty"`
}

type reasoningConfig struct {
	Effort string `json:"effort,omitempty"`
}

// adjustRequestForProvider modifies the chat request based on provider and model
// capabilities. Each provider has different rules for reasoning models.
func (p *ChatProvider) adjustRequestForProvider(body *chatRequest, model string) {
	m := strings.ToLower(model)

	switch {
	case p.isOpenRouter() && isOpenAIReasoning(m):
		// OpenRouter normalizes reasoning params across providers.
		// Use their unified reasoning.effort parameter.
		body.Reasoning = &reasoningConfig{Effort: "medium"}

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
		if strings.HasPrefix(m, prefix) || strings.Contains(m, "/"+prefix) {
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
