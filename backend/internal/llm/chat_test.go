package llm

import "testing"

func TestCleanResponseContent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "clean_bold", in: "**Summary:** all good", want: "**Summary:** all good"},
		{name: "clean_json_array", in: `[{"severity":"high"}]`, want: `[{"severity":"high"}]`},
		{name: "clean_heading", in: "# Review\nLooks good", want: "# Review\nLooks good"},
		{name: "think_tag", in: "<think>internal reasoning</think>actual output", want: "actual output"},
		{name: "reasoning_tag", in: "<reasoning>hmm let me think</reasoning>actual", want: "actual"},
		{name: "thought_tag", in: "<thought>planning</thought>actual", want: "actual"},
		{name: "unclosed_think", in: "<think>reasoning that never ends", want: ""},
		{
			// First <think> to first </think> stripped → "still thinking</think>result"
			// Loop: no more <think> open tags → stops. Marker search finds no structural prefix → returns as-is.
			name: "nested_think",
			in:   "<think>outer<think>inner</think>still thinking</think>result",
			want: "still thinking</think>result",
		},
		{
			name: "reasoning_prefix_bold_marker",
			in:   "The user wants a verdict on the code quality.\n\n**Verdict:** looks fine",
			want: "**Verdict:** looks fine",
		},
		{
			name: "reasoning_prefix_json_marker",
			in:   "Let me analyze this diff carefully.\n\n[{\"severity\":\"low\"}]",
			want: `[{"severity":"low"}]`,
		},
		{
			name: "reasoning_prefix_no_marker",
			in:   "The user wants me to review this code and I think it looks okay overall",
			want: "The user wants me to review this code and I think it looks okay overall",
		},
		{
			name: "code_block_preserved",
			in:   "```go\nfunc main() {}\n```",
			want: "```go\nfunc main() {}\n```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanResponseContent(tt.in)
			if got != tt.want {
				t.Errorf("cleanResponseContent(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsOpenAIReasoning(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"o1", true},
		{"o1-mini", true},
		{"o1-preview", true},
		{"o3", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"openrouter/o1-mini", true},
		{"o1x", false},
		{"o3de", false},
		{"gpt-4o", false},
		{"foo", false},
		{"", false},
	}

	for _, tt := range tests {
		name := tt.model
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			got := isOpenAIReasoning(tt.model)
			if got != tt.want {
				t.Errorf("isOpenAIReasoning(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestIsAnthropicThinking(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-3-thinking", true},
		{"claude-extended", true},
		{"claude-3-sonnet", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := isAnthropicThinking(tt.model)
			if got != tt.want {
				t.Errorf("isAnthropicThinking(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestIsOpenRouter(t *testing.T) {
	tests := []struct {
		name string
		cp   ChatProvider
		want bool
	}{
		{"by_name", ChatProvider{name: "openrouter"}, true},
		{"by_url", ChatProvider{baseURL: "https://openrouter.ai/api/v1"}, true},
		{"not_openrouter", ChatProvider{name: "openai", baseURL: "https://api.openai.com"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cp.isOpenRouter()
			if got != tt.want {
				t.Errorf("isOpenRouter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAdjustRequestForProvider(t *testing.T) {
	t.Run("openrouter_reasoning_model", func(t *testing.T) {
		cp := ChatProvider{name: "openrouter"}
		temp := 0.3
		body := chatRequest{MaxTokens: 4096, Temperature: &temp}
		cp.adjustRequestForProvider(&body, "o1-mini")

		if body.Reasoning == nil {
			t.Fatal("expected reasoning config to be set")
		}
		if !body.Reasoning.Exclude {
			t.Error("expected Exclude=true")
		}
		if body.Reasoning.Effort != "medium" {
			t.Errorf("expected Effort=medium, got %q", body.Reasoning.Effort)
		}
	})

	t.Run("openrouter_non_reasoning", func(t *testing.T) {
		cp := ChatProvider{name: "openrouter"}
		temp := 0.3
		body := chatRequest{MaxTokens: 4096, Temperature: &temp}
		cp.adjustRequestForProvider(&body, "gpt-4o")

		if body.Reasoning == nil || !body.Reasoning.Exclude {
			t.Error("expected Exclude=true for all openrouter models")
		}
		if body.Reasoning.Effort != "" {
			t.Errorf("expected empty Effort for non-reasoning, got %q", body.Reasoning.Effort)
		}
	})

	t.Run("direct_openai_reasoning", func(t *testing.T) {
		cp := ChatProvider{name: "openai"}
		temp := 0.3
		body := chatRequest{MaxTokens: 4096, Temperature: &temp}
		cp.adjustRequestForProvider(&body, "o3-mini")

		if body.MaxCompletionTokens != 4096 {
			t.Errorf("expected MaxCompletionTokens=4096, got %d", body.MaxCompletionTokens)
		}
		if body.MaxTokens != 0 {
			t.Errorf("expected MaxTokens=0, got %d", body.MaxTokens)
		}
		if body.Temperature != nil {
			t.Error("expected Temperature=nil for o-series")
		}
	})

	t.Run("anthropic_thinking", func(t *testing.T) {
		cp := ChatProvider{name: "anthropic"}
		temp := 0.3
		body := chatRequest{MaxTokens: 4096, Temperature: &temp}
		cp.adjustRequestForProvider(&body, "claude-3-thinking")

		if body.Temperature == nil || *body.Temperature != 1.0 {
			t.Errorf("expected Temperature=1.0, got %v", body.Temperature)
		}
	})

	t.Run("standard_model_unchanged", func(t *testing.T) {
		cp := ChatProvider{name: "openai"}
		temp := 0.7
		body := chatRequest{MaxTokens: 2048, Temperature: &temp}
		cp.adjustRequestForProvider(&body, "gpt-4o")

		if body.MaxTokens != 2048 {
			t.Errorf("expected MaxTokens unchanged, got %d", body.MaxTokens)
		}
		if body.Temperature == nil || *body.Temperature != 0.7 {
			t.Errorf("expected Temperature unchanged, got %v", body.Temperature)
		}
		if body.Reasoning != nil {
			t.Error("expected no reasoning config")
		}
		if body.ReasoningEffort != "" {
			t.Errorf("expected no ReasoningEffort for non-reasoning model, got %q", body.ReasoningEffort)
		}
	})

	// gpt-5.x is a reasoning model on Azure. Without an explicit effort, Azure's
	// default consumes the token budget on invisible reasoning. Guard: the adapter
	// forces "minimal" so coordination calls (leadBrief, intent, etc.) get a
	// visible response. Callers can opt up to "low"/"medium" for quality-sensitive
	// stages.
	t.Run("gpt5_defaults_to_minimal_reasoning", func(t *testing.T) {
		cp := ChatProvider{name: "azure"}
		temp := 0.2
		body := chatRequest{MaxTokens: 8000, Temperature: &temp}
		cp.adjustRequestForProvider(&body, "gpt-5.4")

		if body.MaxCompletionTokens != 8000 {
			t.Errorf("expected MaxCompletionTokens=8000, got %d", body.MaxCompletionTokens)
		}
		if body.MaxTokens != 0 {
			t.Errorf("expected MaxTokens=0, got %d", body.MaxTokens)
		}
		if body.ReasoningEffort != "minimal" {
			t.Errorf("expected ReasoningEffort=minimal, got %q", body.ReasoningEffort)
		}
	})

	t.Run("gpt5_explicit_effort_preserved", func(t *testing.T) {
		cp := ChatProvider{name: "azure"}
		temp := 0.2
		// Caller-set effort (simulating specialists passing "low") must survive.
		body := chatRequest{MaxTokens: 4000, Temperature: &temp, ReasoningEffort: "low"}
		cp.adjustRequestForProvider(&body, "gpt-5.4")

		if body.ReasoningEffort != "low" {
			t.Errorf("expected ReasoningEffort=low preserved, got %q", body.ReasoningEffort)
		}
	})

	t.Run("non_gpt5_no_reasoning_effort_default", func(t *testing.T) {
		cp := ChatProvider{name: "azure"}
		temp := 0.2
		body := chatRequest{MaxTokens: 4096, Temperature: &temp}
		cp.adjustRequestForProvider(&body, "gpt-4o-2024-08-06")

		if body.ReasoningEffort != "" {
			t.Errorf("non-gpt-5 model must not get a reasoning_effort default, got %q", body.ReasoningEffort)
		}
	})

	// OpenRouter + o-series: the caller's ReasoningEffort must route to the
	// wrapped `Reasoning.Effort` field (OpenRouter's wire format), not the
	// top-level `reasoning_effort` (which OpenRouter forwards only sometimes).
	// Regression guard: the previous code hardcoded "medium" and silently
	// dropped the caller's value.
	t.Run("openrouter_oseries_propagates_caller_effort", func(t *testing.T) {
		cp := ChatProvider{name: "openrouter"}
		temp := 0.3
		body := chatRequest{MaxTokens: 4096, Temperature: &temp, ReasoningEffort: "low"}
		cp.adjustRequestForProvider(&body, "o1-mini")

		if body.Reasoning == nil || body.Reasoning.Effort != "low" {
			t.Errorf("expected Reasoning.Effort=low, got %+v", body.Reasoning)
		}
		if body.ReasoningEffort != "" {
			t.Errorf("top-level ReasoningEffort must be cleared on OpenRouter, got %q", body.ReasoningEffort)
		}
	})

	// OpenRouter + gpt-5.x: the adapter must route the default/caller effort
	// to the wrapped form, not the top-level field. Previously BOTH got set —
	// contract violation per the struct comment. Low current blast radius (no
	// prod config routes gpt-5 via OpenRouter) but this locks the invariant.
	t.Run("openrouter_gpt5_uses_wrapped_form_only", func(t *testing.T) {
		cp := ChatProvider{name: "openrouter"}
		temp := 0.2
		body := chatRequest{MaxTokens: 8000, Temperature: &temp}
		cp.adjustRequestForProvider(&body, "gpt-5.4")

		if body.Reasoning == nil || body.Reasoning.Effort != "minimal" {
			t.Errorf("expected wrapped Reasoning.Effort=minimal on OpenRouter+gpt-5, got %+v", body.Reasoning)
		}
		if body.ReasoningEffort != "" {
			t.Errorf("top-level ReasoningEffort must be cleared on OpenRouter, got %q", body.ReasoningEffort)
		}
	})

	// Non-OpenRouter providers must NOT carry the wrapped `reasoning` object;
	// Azure only accepts top-level `reasoning_effort`. Locks the invariant that
	// no future branch accidentally sets both.
	t.Run("azure_clears_wrapped_reasoning_object", func(t *testing.T) {
		cp := ChatProvider{name: "azure"}
		temp := 0.2
		// Simulate a scenario where a previous branch populated the wrapped
		// form. The trailing invariant clean-up must clear it for non-OpenRouter
		// providers that set the top-level field.
		body := chatRequest{
			MaxTokens:   8000,
			Temperature: &temp,
			Reasoning:   &reasoningConfig{Exclude: true, Effort: "high"},
		}
		cp.adjustRequestForProvider(&body, "gpt-5.4")

		if body.ReasoningEffort != "minimal" {
			t.Errorf("expected top-level ReasoningEffort=minimal, got %q", body.ReasoningEffort)
		}
		if body.Reasoning != nil {
			t.Errorf("expected wrapped Reasoning to be cleared on Azure path, got %+v", body.Reasoning)
		}
	})
}
