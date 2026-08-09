package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gatewayOnlyFromWire digs providerOptions.gateway.only out of a decoded wire
// body, returning nil when any level is absent. Missing levels are a valid
// expectation (unpinned routing), not a test failure.
func gatewayOnlyFromWire(t *testing.T, body map[string]any) []string {
	t.Helper()
	opts, ok := body["providerOptions"].(map[string]any)
	if !ok {
		return nil
	}
	gw, ok := opts["gateway"].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := gw["only"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Errorf("providerOptions.gateway.only contains non-string %v (%T)", v, v)
			continue
		}
		out = append(out, s)
	}
	return out
}

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
		jsonMode      bool
		// assertions on the parsed request body
		wantKey      map[string]any // exact-equal required
		wantAbsent   []string       // these top-level keys must NOT be present
		wantNested   map[string]any // body["reasoning"].(map) field asserts
		wantNoNested []string       // body["reasoning"] must NOT have these keys
		// wantResponseFormatType asserts body["response_format"]["type"] when set.
		wantResponseFormatType string
		// gatewayOnlyParam, when non-empty, builds the provider through
		// NewVercelGatewayProvider with "?only=<param>" on the base URL.
		gatewayOnlyParam string
		// wantGatewayOnly asserts body["providerOptions"]["gateway"]["only"].
		// A non-nil empty slice asserts the key is absent.
		wantGatewayOnly []string
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
		{
			// Vercel AI Gateway + JSONMode. The gateway rejects the OpenAI
			// spelling "json_object" with HTTP 400, so the adapter must
			// downgrade the type to "json" on the wire.
			name:                   "vercel_gateway_jsonmode_type_json",
			providerName:           "vercel",
			model:                  "anthropic/claude-sonnet-4.6",
			effort:                 ReasoningNone,
			temperature:            0.2,
			jsonMode:               true,
			wantResponseFormatType: "json",
		},
		{
			// Same JSONMode flag on a non-gateway provider must keep the
			// OpenAI spelling — the rewrite is gateway-scoped, not global.
			name:                   "openai_jsonmode_type_json_object",
			providerName:           "openai",
			model:                  "gpt-4o",
			effort:                 ReasoningNone,
			temperature:            0.2,
			jsonMode:               true,
			wantResponseFormatType: "json_object",
		},
		{
			// Gateway routing pinned via ?only= must serialize as the nested
			// providerOptions.gateway.only form. The top-level "gateway" key
			// the gateway silently ignores must never be what we emit.
			name:             "vercel_gateway_only_azure_pinned",
			providerName:     "vercel",
			model:            "openai/gpt-5.4",
			effort:           ReasoningLow,
			temperature:      0.2,
			gatewayOnlyParam: "azure",
			wantGatewayOnly:  []string{"azure"},
			wantAbsent:       []string{"gateway"},
		},
		{
			// The exact body qBraid's stages emit after moving off OpenRouter.
			// gpt-5.x defaults to "minimal", which every gpt-5.6 snapshot
			// behind the gateway rejects with HTTP 400 — clamp to "low".
			name:             "vercel_gateway_gpt56_minimal_clamped_to_low",
			providerName:     "vercel",
			model:            "openai/gpt-5.6-sol",
			effort:           ReasoningNone,
			temperature:      0.2,
			gatewayOnlyParam: "azure",
			wantKey: map[string]any{
				"reasoning_effort":      "low",
				"max_completion_tokens": float64(100),
			},
			wantAbsent:      []string{"temperature", "max_tokens", "reasoning"},
			wantGatewayOnly: []string{"azure"},
		},
		{
			// Direct Azure keeps "minimal" — the clamp is gateway-scoped.
			// Guards against "fix one half of a pair" by pinning the other half.
			name:         "azure_gpt56_keeps_minimal",
			providerName: "azure",
			model:        "gpt-5.6-sol",
			effort:       ReasoningNone,
			temperature:  0.2,
			wantKey:      map[string]any{"reasoning_effort": "minimal"},
		},
		{
			// No ?only= means no routing directive — the gateway picks.
			name:            "vercel_gateway_unpinned_omits_routing",
			providerName:    "vercel",
			model:           "openai/gpt-5.4",
			effort:          ReasoningLow,
			temperature:     0.2,
			wantGatewayOnly: []string{},
			wantAbsent:      []string{"providerOptions", "gateway"},
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

			baseURL := server.URL + tc.baseURLSuffix
			p := NewChatProvider(tc.providerName, "test-key", baseURL)
			if tc.providerName == "vercel" {
				if tc.gatewayOnlyParam != "" {
					baseURL += "?only=" + tc.gatewayOnlyParam
				}
				p = NewVercelGatewayProvider("test-key", baseURL)
			}
			_, err := p.Complete(context.Background(), CompletionRequest{
				Model:           tc.model,
				Messages:        []Message{{Role: "user", Content: "hi"}},
				MaxTokens:       100,
				Temperature:     tc.temperature,
				JSONMode:        tc.jsonMode,
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
			if tc.wantResponseFormatType != "" {
				rf, ok := body["response_format"].(map[string]any)
				if !ok {
					t.Fatalf("wire body missing 'response_format' object; got: %s", captured)
				}
				if got := rf["type"]; got != tc.wantResponseFormatType {
					t.Errorf("wire[response_format][type] = %v, want %v", got, tc.wantResponseFormatType)
				}
			}
			if tc.wantGatewayOnly != nil {
				got := gatewayOnlyFromWire(t, body)
				if len(got) != len(tc.wantGatewayOnly) {
					t.Errorf("wire providerOptions.gateway.only = %v, want %v; body: %s", got, tc.wantGatewayOnly, captured)
				} else {
					for i, want := range tc.wantGatewayOnly {
						if got[i] != want {
							t.Errorf("wire providerOptions.gateway.only[%d] = %q, want %q", i, got[i], want)
						}
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

// TestNewVercelGatewayProvider_OnlyParam covers base-URL parsing for the
// routing pin. The empty-value case is the one that bit: the request path is
// appended to baseURL by concatenation, so leaving "?only=" attached produced
// ".../v1?only=/chat/completions" — every call hit /v1 with the endpoint
// swallowed into the query string.
func TestNewVercelGatewayProvider_OnlyParam(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		suffix   string
		wantOnly []string
	}{
		{"pinned_single", "?only=azure", []string{"azure"}},
		{"pinned_multiple", "?only=azure,openai", []string{"azure", "openai"}},
		{"empty_value_degrades_to_unpinned", "?only=", nil},
		{"whitespace_only_degrades_to_unpinned", "?only=%20", nil},
		{"absent_is_unpinned", "", nil},
	}

	const stub = `{"choices":[{"message":{"content":"ok","role":"assistant"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(stub))
			}))
			defer srv.Close()

			p := NewVercelGatewayProvider("test-key", srv.URL+tc.suffix)
			if _, err := p.Complete(context.Background(), CompletionRequest{
				Model:     "openai/gpt-5.6-sol",
				Messages:  []Message{{Role: "user", Content: "hi"}},
				MaxTokens: 100,
			}); err != nil {
				t.Fatalf("Complete: %v", err)
			}

			// The endpoint must always be reached, whatever the param looked like.
			if gotPath != "/chat/completions" {
				t.Errorf("request path = %q, want %q (baseURL=%q)", gotPath, "/chat/completions", p.baseURL)
			}
			if strings.Contains(p.baseURL, "only") {
				t.Errorf("baseURL still carries the routing param: %q", p.baseURL)
			}
			if len(p.gatewayOnly) != len(tc.wantOnly) {
				t.Fatalf("gatewayOnly = %v, want %v", p.gatewayOnly, tc.wantOnly)
			}
			for i, want := range tc.wantOnly {
				if p.gatewayOnly[i] != want {
					t.Errorf("gatewayOnly[%d] = %q, want %q", i, p.gatewayOnly[i], want)
				}
			}
		})
	}
}
