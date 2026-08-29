package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestRequestLoggingCapturesBodiesStatusAndTrace(t *testing.T) {
	var logs bytes.Buffer
	s := &Server{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	handler := traceIDMiddleware(s.requestLogging(s.panicRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body bytes.Buffer
		_, _ = body.ReadFrom(r.Body)
		writeJSON(w, http.StatusCreated, map[string]any{"received": true, "bytes": body.Len()})
	}))))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/installations/1/provider-keys?token=do-not-log&sig=do-not-log-sig&view=full", strings.NewReader(`{"name":"argus","api_key":"body-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d", rr.Code)
	}
	traceID := rr.Header().Get("X-Argus-Trace-Id")
	if traceID == "" {
		t.Fatal("missing response trace ID")
	}
	got := logs.String()
	for _, want := range []string{"inbound HTTP request started", "inbound HTTP request body", "inbound HTTP response body", "inbound HTTP request completed", traceID, `\"name\":\"argus\"`, `"status_code":201`, "view=full"} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "do-not-log") || strings.Contains(got, "do-not-log-sig") || strings.Contains(got, "body-secret") {
		t.Fatalf("credential leaked: %s", got)
	}
}

func TestPanicRecoveryWritesStructuredStackAnd500(t *testing.T) {
	var logs bytes.Buffer
	s := &Server{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	handler := traceIDMiddleware(s.requestLogging(s.panicRecovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rr.Code)
	}
	got := logs.String()
	for _, want := range []string{"HTTP handler panic", "HTTP panic stack", "boom", `"status_code":500`} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs missing %q: %s", want, got)
		}
	}
}

func TestServeHTTPOperationRecordsActualErrorStatus(t *testing.T) {
	var logs bytes.Buffer
	router := chi.NewRouter()
	router.Get("/denied", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "denied"})
	})
	s := &Server{router: router, logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/denied", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rr.Code)
	}
	got := logs.String()
	if !strings.Contains(got, `"status_code":403`) || !strings.Contains(got, `"action":"denial"`) {
		t.Fatalf("operation log missed response status: %s", got)
	}
}
