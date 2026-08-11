package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

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
