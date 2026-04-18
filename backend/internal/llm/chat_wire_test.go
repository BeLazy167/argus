package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestComplete_WireFormat locks the serialized JSON body shape per model +
// provider combination. adjustRequestForProvider is already tested at 100%
// via field-level assertions on chatRequest, but those tests can miss:
//
//  1. Struct-tag typos like `json:"reasoning_effort"` → `json:"reasoningEffort"`.
//  2. Accidental `omitempty` removal that leaks empty fields onto the wire.
//  3. Zero-value fields that serialize unexpectedly (e.g. *float64 = nil).
//
// This test fires real HTTP requests at a httptest.NewServer that captures
// the body, decodes it into a generic map, and asserts keys/values per case.
// Response is a minimal valid chatResponse so Complete() returns cleanly and
// the test isn't blocked on error-handling paths.
func TestComplete_WireFormat(t *testing.T) {
	t.Parallel()

	const stubResponse = `{
		"choices":[{"message":{"content":"ok","role":"assistant"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
	}`

	type wireCase struct {
		name          string
		providerName  string
		baseURLSuffix string // appended to server.URL; affects isOpenRouter detection
		model         string
		effort        ReasoningEffort
		temperature   float64
		// assertions on the parsed request body
		wantKey      map[string]any  // exact-equal required
		wantAbsent   []string        // these top-level keys must NOT be present
		wantNested   map[string]any  // body["reasoning"].(map) field asserts
		wantNoNested []string        // body["reasoning"] must NOT have these keys
	}

	cases := []wireCase{
		{
			// Azure direct + gpt-5.x + no caller effort. Adapter must apply
			// the "minimal" default, strip temperature (gpt-5 rejects it),
			// swap max_tokens → max_completion_tokens.
			name:         "azure_gpt5_default_minimal",
			providerName: "azure",
			model:        "gpt-5.4",
			effort:       ReasoningNone,
			temperature:  0.2,
			wantKey: map[string]any{
				"model":                 "gpt-5.4",
				"reasoning_effort":      "minimal",
				"max_completion_tokens": float64(100),
			},
			wantAbsent: []string{"temperature", "max_tokens", "reasoning"},
		},
		{
			// Azure + gpt-5.x + caller sets "low" — must pass through unchanged.
			name:         "azure_gpt5_explicit_low",
			providerName: "azure",
			model:        "gpt-5.4",
			effort:       ReasoningLow,
			temperature:  0.2,
			wantKey: map[string]any{
				"reasoning_effort":      "low",
				"max_completion_tokens": float64(100),
			},
			wantAbsent: []string{"temperature", "max_tokens", "reasoning"},
		},
		{
			// Non-reasoning model — temperature survives, no reasoning_effort,
			// max_tokens (not max_completion_tokens).
			name:         "azure_gpt4o_normal_temperature",
			providerName: "azure",
			model:        "gpt-4o",
			effort:       ReasoningNone,
			temperature:  0.7,
			wantKey: map[string]any{
				"temperature": 0.7,
				"max_tokens":  float64(100),
			},
			wantAbsent: []string{"reasoning_effort", "max_completion_tokens", "reasoning"},
		},
		{
			// Direct OpenAI + o-series — max_completion_tokens but NO
			// reasoning_effort (o-series uses default medium through a
			// different code path).
			name:         "openai_o3_no_reasoning_effort",
			providerName: "openai",
			model:        "o3-mini",
			effort:       ReasoningNone,
			temperature:  0.3,
			wantKey: map[string]any{
				"max_completion_tokens": float64(100),
			},
			wantAbsent: []string{"temperature", "max_tokens", "reasoning_effort"},
		},
		{
			// OpenRouter + gpt-5.x — the wrapped `reasoning: {effort, exclude}`
			// form wins; top-level reasoning_effort must be cleared.
			name:          "openrouter_gpt5_wrapped_form",
			providerName:  "openrouter",
			baseURLSuffix: "",
			model:         "gpt-5.4",
			effort:        ReasoningNone,
			temperature:   0.2,
			wantAbsent:    []string{"reasoning_effort", "max_tokens"},
			wantNested: map[string]any{
				"effort":  "minimal",
				"exclude": true,
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var captured []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("reading request body: %v", err)
				}
				captured = b
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(stubResponse))
			}))
			defer server.Close()

			p := NewChatProvider(tc.providerName, "test-key", server.URL+tc.baseURLSuffix)
			_, err := p.Complete(context.Background(), CompletionRequest{
				Model:           tc.model,
				Messages:        []Message{{Role: "user", Content: "hi"}},
				MaxTokens:       100,
				Temperature:     tc.temperature,
				ReasoningEffort: tc.effort,
			})
			if err != nil {
				t.Fatalf("Complete returned error: %v", err)
			}

			var body map[string]any
			if err := json.Unmarshal(captured, &body); err != nil {
				t.Fatalf("decoding wire body: %v\nbody=%s", err, captured)
			}

			for k, want := range tc.wantKey {
				got, ok := body[k]
				if !ok {
					t.Errorf("wire body missing key %q; got: %s", k, captured)
					continue
				}
				if got != want {
					t.Errorf("wire[%q] = %v (%T), want %v (%T)", k, got, got, want, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if _, ok := body[absent]; ok {
					t.Errorf("wire body must NOT include key %q; got: %s", absent, captured)
				}
			}
			if len(tc.wantNested) > 0 {
				nested, ok := body["reasoning"].(map[string]any)
				if !ok {
					t.Fatalf("wire body missing nested 'reasoning' object; got: %s", captured)
				}
				for k, want := range tc.wantNested {
					if got := nested[k]; got != want {
						t.Errorf("wire[reasoning][%q] = %v, want %v", k, got, want)
					}
				}
			}
			for _, absent := range tc.wantNoNested {
				if nested, ok := body["reasoning"].(map[string]any); ok {
					if _, has := nested[absent]; has {
						t.Errorf("wire[reasoning] must NOT include key %q", absent)
					}
				}
			}
		})
	}
}
