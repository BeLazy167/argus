package pipeline

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHTTPMermaidValidatorDisabledDoesNotSendPrivateEvidence(t *testing.T) {
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected validator request")
	})}
	validator := NewHTTPMermaidValidator("", "", client)

	err := validator.Validate(context.Background(), "sequenceDiagram\n  private->>evidence: secret")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Validate() error = %v, want disabled validator error", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("disabled validator sent private evidence in %d request(s)", requests.Load())
	}
}

func TestHTTPMermaidValidatorFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{name: "valid", status: http.StatusOK, body: `{"valid":true,"version":"11.13.0"}`},
		{name: "parser rejection", status: http.StatusUnprocessableEntity, body: `{"valid":false,"version":"11.13.0","error":"invalid_syntax"}`, wantErr: true},
		{name: "malformed response", status: http.StatusOK, body: `not-json`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			validator := NewHTTPMermaidValidator(server.URL, "test-secret", http.DefaultClient)
			err := validator.Validate(context.Background(), "flowchart TD\n N1 --> N2")
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestHTTPMermaidValidatorRejectsOversizeWithoutRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"valid":true}`))
	}))
	defer server.Close()
	validator := NewHTTPMermaidValidator(server.URL, "test-secret", http.DefaultClient)
	if err := validator.Validate(context.Background(), strings.Repeat("x", maxMermaidSourceBytes+1)); err == nil {
		t.Fatal("oversized source passed")
	}
	if requests.Load() != 0 {
		t.Fatalf("oversized source made %d request(s)", requests.Load())
	}
}

func TestHTTPMermaidValidatorDoesNotForwardSecretAcrossRedirect(t *testing.T) {
	var redirectedRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		if r.Header.Get("X-Argus-Mermaid-Secret") != "" {
			t.Error("validator secret reached redirect target")
		}
		_, _ = w.Write([]byte(`{"valid":true,"version":"11.13.0"}`))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	validator := NewHTTPMermaidValidator(source.URL, "test-secret", http.DefaultClient)
	if err := validator.Validate(context.Background(), "flowchart TD\n N1 --> N2"); err == nil {
		t.Fatal("redirecting validator was accepted")
	}
	if redirectedRequests.Load() != 0 {
		t.Fatalf("validator followed %d redirect(s)", redirectedRequests.Load())
	}
}
