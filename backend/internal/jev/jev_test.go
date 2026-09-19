// jev_test.go: wire contract for the System One endpoint — request shape,
// auth header, batched answers, cost math, and error statuses.
package jev

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEvaluate_WireShape(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody evalRequest
	var gotState map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var wire struct {
			Model     string              `json:"model"`
			State     map[string]any      `json:"state"`
			Questions map[string]Question `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotBody = evalRequest{Model: wire.Model, Questions: wire.Questions}
		gotState = wire.State
		w.Header().Set("Content-Type", "application/json")
		p := 0.99
		_ = json.NewEncoder(w).Encode(evalResponse{
			Model: "jev-1.13.0",
			Answers: map[string]Answer{
				"addressed": {Type: TypeNoul, Noul: &p},
			},
			Usage: Usage{InputTokens: 1000, OutputTokens: 20},
		})
	}))
	defer srv.Close()

	c := NewClient("ts-key", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	res, err := c.Evaluate(context.Background(),
		map[string]any{"diff": "+x", "n": 42},
		map[string]Question{"addressed": NoulQuestion("fixed?", "yes", "no")},
		"addressed_judge")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	// The state payload must reach the wire decoded — a string or double
	// encoding would prove state is marshaled more than once per call.
	if gotState["diff"] != "+x" || gotState["n"] != float64(42) {
		t.Errorf("state on wire = %v, want verbatim payload", gotState)
	}
	if gotPath != "/v1/systemone" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer ts-key" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody.Model != DefaultModel {
		t.Errorf("model = %q, want pinned %q", gotBody.Model, DefaultModel)
	}
	q, ok := gotBody.Questions["addressed"]
	if !ok || q.Type != TypeNoul {
		t.Fatalf("question missing/wrong type: %+v", gotBody.Questions)
	}
	crit, ok := q.Criteria.(map[string]any)
	if !ok || crit["true"] != "yes" || crit["false"] != "no" {
		t.Fatalf("criteria malformed: %+v", q.Criteria)
	}

	p := res.Noul("addressed")
	if p == nil || *p != 0.99 {
		t.Fatalf("noul = %v", p)
	}
	if res.Noul("missing") != nil {
		t.Fatal("missing answer must return nil, not 0")
	}
	wantCost := 1000 * inputCostPerToken
	if res.Cost != wantCost {
		t.Fatalf("cost = %v, want %v (input-only billing)", res.Cost, wantCost)
	}
}

func TestEvaluate_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	_, err := c.Evaluate(context.Background(), "s", map[string]Question{}, "s")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("err = %v, want 429 surfaced for escalation", err)
	}
}

func TestEvaluate_BadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.Evaluate(context.Background(), "s", map[string]Question{}, "s"); err == nil {
		t.Fatal("malformed response must error")
	}
}

func TestEvaluate_OversizedStateFailsBeforeWire(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	big := map[string]any{"blob": strings.Repeat("x", maxStateBytes)}
	_, err := c.Evaluate(context.Background(), big, map[string]Question{}, "s")
	if err == nil || !strings.Contains(err.Error(), "state too large") {
		t.Fatalf("err = %v, want state-too-large escalation", err)
	}
	if hits != 0 {
		t.Fatal("oversized state must fail fast, never reach the wire")
	}
}

func TestEvaluate_NegativeUsageClamps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(evalResponse{
			Model:   "jev-1.13.0",
			Answers: map[string]Answer{},
			Usage:   Usage{InputTokens: -500, OutputTokens: -20},
		})
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	res, err := c.Evaluate(context.Background(), "s", map[string]Question{}, "s")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.Usage.InputTokens != 0 || res.Usage.OutputTokens != 0 || res.Cost != 0 {
		t.Fatalf("negative usage must clamp to zero, got %+v cost=%v", res.Usage, res.Cost)
	}
}

func TestEvaluate_OversizedResponseErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Just past the cap — a bounded read must cut this off, not OOM.
		_, _ = w.Write([]byte(`{"pad":"` + strings.Repeat("x", maxResponseBytes) + `"}`))
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	if _, err := c.Evaluate(context.Background(), "s", map[string]Question{}, "s"); err == nil {
		t.Fatal("response past maxResponseBytes must error")
	}
}

func TestResult_NoulChoiceGuards(t *testing.T) {
	nan := math.NaN()
	res := Result{Answers: map[string]Answer{
		"good":         {Type: TypeNoul, Noul: ptr(0.9)},
		"out_high":     {Type: TypeNoul, Noul: ptr(1.01)},
		"out_neg":      {Type: TypeNoul, Noul: ptr(-0.01)},
		"nan":          {Type: TypeNoul, Noul: &nan},
		"wrong_type":   {Type: TypeChoice, Choice: "deep", Probabilities: map[string]float64{"deep": 0.9}},
		"choice_ok":    {Type: TypeChoice, Choice: "deep", Probabilities: map[string]float64{"deep": 0.8, "skim": 0.2}},
		"choice_nomap": {Type: TypeChoice, Choice: "deep"}, // picked but no probability for it
	}}

	if p := res.Noul("good"); p == nil || *p != 0.9 {
		t.Errorf("noul(good) = %v", p)
	}
	for _, id := range []string{"out_high", "out_neg", "nan", "wrong_type", "missing"} {
		if res.Noul(id) != nil {
			t.Errorf("noul(%s) must be nil for out-of-range/missing/wrong-type", id)
		}
	}
	if c, p, ok := res.Choice("choice_ok"); !ok || c != "deep" || p != 0.8 {
		t.Errorf("choice(choice_ok) = %q %.2f %v", c, p, ok)
	}
	for _, id := range []string{"choice_nomap", "good", "missing"} {
		if _, _, ok := res.Choice(id); ok {
			t.Errorf("choice(%s) must fail for missing probability/wrong-type/missing", id)
		}
	}
}

func ptr(f float64) *float64 { return &f }
