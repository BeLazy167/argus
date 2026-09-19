// jev_test.go: wire contract for the System One endpoint — request shape,
// auth header, batched answers, cost math, and error statuses.
package jev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEvaluate_WireShape(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody evalRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
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
		map[string]any{"diff": "+x"},
		map[string]Question{"addressed": NoulQuestion("fixed?", "yes", "no")},
		"addressed_judge")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
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
