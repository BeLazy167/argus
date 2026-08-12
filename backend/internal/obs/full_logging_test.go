package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestContextHandlerAddsTraceIDToStdoutRecord(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&logs, nil)))
	ctx := SetTraceID(context.Background(), "4d76ba82-6057-4ea9-b234-8a84f169c05c")
	logger.InfoContext(ctx, "correlated")
	if !strings.Contains(logs.String(), `"trace_id":"4d76ba82-6057-4ea9-b234-8a84f169c05c"`) {
		t.Fatalf("log missing context trace: %s", logs.String())
	}
}

func TestContextHandlerExplicitBoundTraceTakesPrecedence(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(NewContextHandler(slog.NewJSONHandler(&logs, nil))).With("trace_id", "explicit-trace")
	ctx := SetTraceID(context.Background(), "context-trace")
	logger.InfoContext(ctx, "correlated")
	got := logs.String()
	if !strings.Contains(got, `"trace_id":"explicit-trace"`) || strings.Contains(got, "context-trace") {
		t.Fatalf("explicit bound trace did not take precedence: %s", got)
	}
	if strings.Count(got, `"trace_id"`) != 1 {
		t.Fatalf("duplicate trace_id fields: %s", got)
	}
}

func TestLogPayloadChunksAndRedactsCredentials(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	payload := []byte(`{"api_key":"secret-key","output":"` + strings.Repeat("complete-model-output", 600) + `"}`)
	LogPayload(context.Background(), logger, "payload", "op-1", "response", "application/json", payload)
	got := logs.String()
	if strings.Contains(got, "secret-key") {
		t.Fatalf("credential leaked: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, `"chunk_count":`) {
		t.Fatalf("missing redaction/chunk metadata: %s", got)
	}
	if strings.Count(got, `"msg":"payload"`) < 2 {
		t.Fatalf("large payload was not chunked: %s", got)
	}
}

func TestPayloadStreamKeepsUTF8ChunksReconstructable(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	stream := NewPayloadStream(context.Background(), logger, "stream", "op", "response", "text/plain")
	input := strings.Repeat("a", payloadChunkBytes-1) + "😀" + strings.Repeat("b", 20)
	_, _ = stream.Write([]byte(input))
	stream.Finish()
	var rebuilt strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["encoding"] != "utf-8" {
			t.Fatalf("chunk encoding = %v", record["encoding"])
		}
		rebuilt.WriteString(record["payload"].(string))
	}
	if rebuilt.String() != input {
		t.Fatalf("rebuilt payload differs: got %d bytes, want %d", rebuilt.Len(), len(input))
	}
}

func TestPayloadLogLinesStayBelowFlyLimitAfterWorstCaseEscaping(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	payload := bytes.Repeat([]byte{0x01}, payloadChunkBytes*2)
	LogPayload(context.Background(), logger, "payload", "op", "result", "application/octet-stream", payload)
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if len(line) >= 16<<10 {
			t.Fatalf("serialized line is %d bytes", len(line))
		}
	}
}

func TestLoggingRoundTripperDoesNotPreconsumeStreamingRequest(t *testing.T) {
	reader, writer := io.Pipe()
	baseEntered := make(chan struct{})
	transport := &LoggingRoundTripper{Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			close(baseEntered)
			_, err := io.Copy(io.Discard, req.Body)
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
		})}
	req, _ := http.NewRequest(http.MethodPost, "https://stream.example/upload", reader)
	done := make(chan error, 1)
	go func() {
		resp, err := transport.RoundTrip(req)
		if err == nil {
			err = resp.Body.Close()
		}
		done <- err
	}()
	<-baseEntered
	_, _ = writer.Write([]byte("streaming payload"))
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLoggingRoundTripperPreservesBodiesAndLogsCompleteExchange(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != `{"prompt":"hello"}` {
			t.Fatalf("transport request body = %q", body)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer usable-secret" {
			t.Fatalf("transport authorization = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"output":"complete answer"}`)),
			Request:    req,
		}, nil
	})
	transport := &LoggingRoundTripper{Base: base, Service: "test-model", Logger: logger}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://model.example/v1/chat", strings.NewReader(`{"prompt":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer usable-secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"output":"complete answer"}` {
		t.Fatalf("caller response body = %q", body)
	}
	got := logs.String()
	for _, want := range []string{"outbound HTTP request started", `\"prompt\":\"hello\"`, "complete answer", "outbound HTTP request completed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "usable-secret") {
		t.Fatalf("authorization leaked: %s", got)
	}
}

func TestLoggingRoundTripperRedactsPostHogBodyAPIKey(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "usable-posthog-key") {
			t.Fatalf("transport body was changed: %q", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	})
	transport := &LoggingRoundTripper{Base: base, Service: "posthog", Logger: logger}
	req, _ := http.NewRequest(http.MethodPost, "https://posthog.example/batch", strings.NewReader(`{"api_key":"usable-posthog-key","batch":[]}`))
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	got := logs.String()
	if strings.Contains(got, "usable-posthog-key") {
		t.Fatalf("PostHog API key leaked: %s", got)
	}
	if !strings.Contains(got, "REDACTED") {
		t.Fatalf("redaction missing: %s", got)
	}
}
