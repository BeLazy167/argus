// Package jev is a minimal client for TypeSafe's System One endpoint (Jev).
//
// Jev is a classifier, not a chat model: it takes a `state` plus a map of
// typed questions (noul = yes/no probability, choice = pick from a set,
// score = ordered scale) and returns probabilities. It cannot emit prose, so
// it never implements llm.Provider — it sits in front of generative stages as
// a cheap pre-filter: act on confident answers, escalate the uncertain band
// to the real model.
//
// Calibration notes that shape callers' thresholds (public benchmarks + our
// own runs): schema-valid is not correct; act only at >=0.95-0.97, treat the
// middle band as "ask the expensive model", and never send arithmetic.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
	"github.com/BeLazy167/argus/backend/internal/util"
)

const (
	// DefaultBaseURL is the TypeSafe API root; the endpoint path is appended.
	DefaultBaseURL = "https://api.typesafe.ai"
	// ProviderName is the provider_keys slot for per-installation BYOK Jev
	// keys (Settings → Providers → TypeSafe Jev).
	ProviderName = "typesafe"
	// DefaultModel pins the versioned model id. Aliases (jev-latest) move on
	// release and silently change the answers behind tuned thresholds.
	DefaultModel = "jev-1.13.0"
	// inputCostPerToken is Jev's billed rate ($0.042/Mtok input; output free).
	inputCostPerToken = 0.042 / 1_000_000
	// clientTimeout bounds one eval call. Jev answers in ~200-300ms; this only
	// guards callers that forgot a context deadline.
	clientTimeout = 15 * time.Second
	// maxStateBytes pre-flights the marshaled state against the 32k-token
	// state cap (~4 chars/token). Oversized states fail fast here instead of
	// paying a doomed 422 round-trip; callers escalate on the error.
	maxStateBytes = 120 * 1024
	// maxResponseBytes bounds the response read. A malformed or hostile
	// endpoint streaming an unbounded body must not OOM the worker —
	// anything past the cap fails the JSON decode and escalates.
	maxResponseBytes = 4 << 20
)

// Question types accepted by the endpoint.
const (
	TypeNoul   = "noul"
	TypeChoice = "choice"
	TypeScore  = "score"
)

// Question is one typed question in an eval request. Criteria's wire shape
// depends on Type: object {"true":..,"false":..} for noul, map[string]string
// for choice, []string for score — hence `any`.
type Question struct {
	Type string `json:"type"`
	// Instructions is optional per the API — omitempty matches the official
	// SDKs, which omit the key rather than send null.
	Instructions any `json:"instructions,omitempty"`
	Criteria     any `json:"criteria,omitempty"`
}

// NoulQuestion builds a yes/no question with explicit criteria for both
// branches. Keep criteria concrete — a soft branch ("...or too little to
// tell") is an escape hatch Jev will take.
func NoulQuestion(instructions, criteriaTrue, criteriaFalse string) Question {
	return Question{
		Type:         TypeNoul,
		Instructions: instructions,
		Criteria:     map[string]string{"true": criteriaTrue, "false": criteriaFalse},
	}
}

// Answer is one question's result, keyed under the question id the caller
// chose. Only the field matching Type is populated; Noul is a pointer so a
// missing answer is distinguishable from a confident "no".
type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	// Legend maps score level indexes back to their descriptions. Unused
	// today (no caller asks score questions) but kept so the wire shape is
	// fully modeled.
	Legend map[string]string `json:"legend,omitempty"`
}

// Usage mirrors the endpoint's token accounting. Output tokens are billed at
// zero but still reported.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Result is one eval call: the versioned model that answered, per-question
// answers, usage, and the computed dollar cost.
type Result struct {
	Model   string
	Answers map[string]Answer
	Usage   Usage
	Cost    float64
}

// Noul returns the yes-probability for id, or nil when the answer is absent,
// not a noul, or outside [0,1] — encoding/json happily decodes 1.7 and 1e400
// (+Inf), and an out-of-range probability must never clear a caller's
// threshold. Escalate on nil rather than guessing.
func (r Result) Noul(id string) *float64 {
	a, ok := r.Answers[id]
	if !ok || a.Type != TypeNoul || a.Noul == nil {
		return nil
	}
	p := *a.Noul
	if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
		return nil
	}
	return a.Noul
}

// Choice returns the picked option and ITS probability (not the derived
// confidence field — the raw top-of-softmax is what thresholds should gate
// on). ok is false when the answer is absent, not a choice, malformed, or the
// probability is outside [0,1].
func (r Result) Choice(id string) (choice string, probability float64, ok bool) {
	a, ok := r.Answers[id]
	if !ok || a.Type != TypeChoice || a.Choice == "" {
		return "", 0, false
	}
	p, ok := a.Probabilities[a.Choice]
	if !ok || math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
		return "", 0, false
	}
	return a.Choice, p, true
}

// Client calls POST {baseURL}/v1/systemone. A single Evaluate call batches
// independent questions over one state — TypeSafe measures ~12x cheaper and
// ~10x faster than per-question calls, so callers should never loop.
type Client struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// Option customizes a Client; only tests and self-hosted gateways need them.
type Option func(*Client)

// WithBaseURL overrides the API root (httptest servers, proxies).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient overrides the HTTP client (custom transports in tests).
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.client = hc } }

// WithModel overrides the pinned model id.
func WithModel(m string) Option { return func(c *Client) { c.model = m } }

// NewClient builds a Jev client. apiKey is the TypeSafe key (TYPESAFE_API_KEY).
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: DefaultBaseURL,
		model:   DefaultModel,
		client:  &http.Client{Timeout: clientTimeout, Transport: obs.NewLoggingRoundTripper("jev", http.DefaultTransport)},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type evalRequest struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

type evalResponse struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// Evaluate runs one batched eval: every question in `questions` is answered
// against `state` in parallel. `stage` labels the pipeline step that owns the
// call for telemetry parity with llm.ChatProvider — it is never sent on the
// wire. A non-200 response or malformed body is an error; callers decide
// whether to escalate (there is no retry — the fallback IS the retry).
func (c *Client) Evaluate(ctx context.Context, state any, questions map[string]Question, stage string) (Result, error) {
	operationID := obs.NewLogID()
	start := time.Now()
	// Marshal state once: the bytes serve the size pre-flight AND ride the
	// request envelope as RawMessage — no second walk over the state tree.
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return Result{}, fmt.Errorf("marshaling state: %w", err)
	}
	if len(stateJSON) > maxStateBytes {
		slog.WarnContext(ctx, "jev state over cap, escalating",
			slog.String("event", "jev.state.oversized"),
			slog.String("stage", stage),
			slog.Int("state_bytes", len(stateJSON)),
			slog.Int("cap_bytes", maxStateBytes),
			slog.Int("question_count", len(questions)),
			slog.String("trace_id", obs.TraceID(ctx)),
		)
		// Emit the same failure record every other error path does —
		// oversized calls must be visible in jev.call.* telemetry parity.
		slog.ErrorContext(ctx, "jev call failed",
			slog.String("event", "jev.call.failed"),
			slog.String("operation_id", operationID),
			slog.String("provider", "typesafe"),
			slog.String("model", c.model),
			slog.String("stage", stage),
			slog.String("error", "state too large"),
			slog.Int("state_bytes", len(stateJSON)),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.String("trace_id", obs.TraceID(ctx)),
		)
		return Result{}, fmt.Errorf("state too large: %d bytes > %d", len(stateJSON), maxStateBytes)
	}
	body := evalRequest{Model: c.model, State: json.RawMessage(stateJSON), Questions: questions}
	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, fmt.Errorf("marshaling request: %w", err)
	}
	obs.LogPayload(ctx, slog.Default(), "Jev eval request", operationID, "request", "application/json", payload)
	slog.InfoContext(ctx, "Jev eval started",
		"operation_id", operationID,
		"provider", "typesafe",
		"model", c.model,
		"stage", stage,
		"question_count", len(questions),
	)

	res, statusCode, err := c.evaluate(ctx, operationID, payload)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		slog.ErrorContext(ctx, "jev call failed",
			slog.String("event", "jev.call.failed"),
			slog.String("operation_id", operationID),
			slog.String("provider", "typesafe"),
			slog.String("model", c.model),
			slog.String("stage", stage),
			slog.String("error", err.Error()),
			slog.Int("status_code", statusCode),
			slog.Int64("duration_ms", durationMs),
			slog.String("trace_id", obs.TraceID(ctx)),
		)
		return Result{}, err
	}
	slog.InfoContext(ctx, "jev call completed",
		slog.String("event", "jev.call.completed"),
		slog.String("operation_id", operationID),
		slog.String("provider", "typesafe"),
		slog.String("model", res.Model),
		slog.String("stage", stage),
		slog.Int("prompt_tokens", res.Usage.InputTokens),
		slog.Int("completion_tokens", res.Usage.OutputTokens),
		slog.Float64("cost_usd", res.Cost),
		slog.Int64("duration_ms", durationMs),
		slog.String("trace_id", obs.TraceID(ctx)),
	)
	return res, nil
}

// ValidBaseURL accepts only absolute http(s) URLs with a host — the stored
// value names an egress endpoint for tenant code, so anything else (bare
// hosts, paths without scheme, non-http schemes) is rejected.
func ValidBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// evaluate is the untyped inner call (telemetry lives in Evaluate; the body
// arrives pre-marshaled so state is serialized once for the whole call).
// Returns the parsed result, the HTTP status (0 for pre-response failures),
// and error.
func (c *Client) evaluate(ctx context.Context, operationID string, jsonBody []byte) (Result, int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/systemone", bytes.NewReader(jsonBody))
	if err != nil {
		return Result{}, 0, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return Result{}, 0, fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Result{}, resp.StatusCode, fmt.Errorf("reading response: %w", err)
	}
	if len(respBody) > maxResponseBytes {
		return Result{}, resp.StatusCode, fmt.Errorf("jev response too large: > %d bytes", maxResponseBytes)
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, resp.StatusCode, fmt.Errorf("jev API error (status %d): %s", resp.StatusCode, util.Truncate(string(respBody), 500, true))
	}

	var parsed evalResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return Result{}, resp.StatusCode, fmt.Errorf("unmarshaling response: %w", err)
	}
	if payload, err := json.Marshal(parsed); err == nil {
		obs.LogPayload(ctx, slog.Default(), "Jev eval response", operationID, "response", "application/json", payload)
	}
	model := parsed.Model
	if model == "" {
		model = c.model
	}
	// Negative usage is garbage, not a refund — clamp before it can cancel
	// out real spend in a stage bucket or the run total.
	if parsed.Usage.InputTokens < 0 {
		parsed.Usage.InputTokens = 0
	}
	if parsed.Usage.OutputTokens < 0 {
		parsed.Usage.OutputTokens = 0
	}
	return Result{
		Model:   model,
		Answers: parsed.Answers,
		Usage:   parsed.Usage,
		Cost:    float64(parsed.Usage.InputTokens) * inputCostPerToken,
	}, resp.StatusCode, nil
}
