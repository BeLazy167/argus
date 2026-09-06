package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestSkipBodyLogging(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		skip bool
	}{
		{"/webhooks/github", true},
		{"/mcp", true},
		{"/mcp/", true},
		{"/mcp/anything", true},
		{"/mcpx", false},
		{"/api/v1/reviews", false},
		{"/", false},
	}
	for _, tt := range tests {
		if got := skipBodyLogging(tt.path); got != tt.skip {
			t.Errorf("skipBodyLogging(%q) = %v, want %v", tt.path, got, tt.skip)
		}
	}
}

// Neither marker may appear in any log record — MCP requests and responses
// carry memory content and review findings derived from private source code —
// and that must hold for tool errors and auth failures too, not just 200s.
func TestRequestLoggingOmitsMCPBodies(t *testing.T) {
	t.Parallel()
	const reqMarker = "REQ-MARKER-8f3a-MUST-NOT-LOG"
	const respMarker = "RESP-MARKER-2c1d-MUST-NOT-LOG"
	var logs bytes.Buffer
	s := &Server{logger: slog.New(slog.NewJSONHandler(&logs, nil))}

	for _, tc := range []struct {
		name   string
		path   string
		status int
	}{
		{"ok", "/mcp", http.StatusOK},
		{"trailing slash", "/mcp/", http.StatusOK},
		{"auth failure", "/mcp", http.StatusUnauthorized},
		{"tool error", "/mcp", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs.Reset()
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), reqMarker) {
					t.Fatal("handler did not receive the request body — exclusion must not consume it")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"body":"` + respMarker + `"}`))
			})
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"q":"`+reqMarker+`"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.requestLogging(inner).ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			out := logs.String()
			if strings.Contains(out, reqMarker) || strings.Contains(out, respMarker) {
				t.Fatalf("a body marker reached the log:\n%s", out)
			}
			if !strings.Contains(out, "inbound HTTP request completed") || !strings.Contains(out, `"status_code":`+strconv.Itoa(tc.status)) {
				t.Fatalf("the completion record with status must still be written:\n%s", out)
			}
		})
	}

	// Control: a non-excluded path still logs its body, proving the assertions
	// above are exercising body logging at all. The streaming logger only
	// records bytes the handler actually reads, so the handler must consume
	// the body — same as the excluded-path subtests above.
	logs.Reset()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/anything", strings.NewReader(reqMarker))
	req.Header.Set("Content-Type", "text/plain")
	s.requestLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	})).ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(logs.String(), reqMarker) {
		t.Fatal("control path did not log its body; the exclusion assertions are vacuous")
	}
}
