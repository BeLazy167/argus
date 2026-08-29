package obs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Fly truncates oversized individual log lines. Payloads are therefore emitted
// as a sequence of small, independently searchable JSON records.
// 1536 raw bytes remain below a 16 KiB Fly line even when JSON escaping
// expands every byte to a six-character Unicode escape.
const payloadChunkBytes = 1536

// LogWriter converts dependency writes into one structured log record. It
// prevents standard-library servers from writing plaintext to stderr.
type LogWriter struct {
	logger  *slog.Logger
	level   slog.Level
	message string
}

func NewLogWriter(logger *slog.Logger, level slog.Level, message string) *LogWriter {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogWriter{logger: logger, level: level, message: message}
}
func (w *LogWriter) Write(p []byte) (int, error) {
	w.logger.Log(context.Background(), w.level, w.message, "message", strings.TrimSpace(string(p)))
	return len(p), nil
}

// PrintfLogger adapts printf-style dependency logs to structured slog output.
// It is used for libraries that otherwise write plaintext to stderr.
type PrintfLogger struct{ logger *slog.Logger }

func NewPrintfLogger(logger *slog.Logger) *PrintfLogger {
	if logger == nil {
		logger = slog.Default()
	}
	return &PrintfLogger{logger: logger}
}
func (l *PrintfLogger) Debugf(format string, args ...interface{}) {
	l.logger.Debug("dependency log", "message", fmt.Sprintf(format, args...))
}
func (l *PrintfLogger) Logf(format string, args ...interface{}) {
	l.logger.Info("dependency log", "message", fmt.Sprintf(format, args...))
}
func (l *PrintfLogger) Warnf(format string, args ...interface{}) {
	l.logger.Warn("dependency log", "message", fmt.Sprintf(format, args...))
}
func (l *PrintfLogger) Errorf(format string, args ...interface{}) {
	l.logger.Error("dependency log", "message", fmt.Sprintf(format, args...))
}

// NewLogID returns a short correlation ID for one operation. It is deliberately
// independent of request trace IDs: one request can make many concurrent calls.
func NewLogID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return time.Now().UTC().Format("20060102T150405.000000000")
}

// ContextHandler adds correlation attributes carried in context to every
// stdout record. Explicit record attributes remain authoritative.
type ContextHandler struct {
	inner     slog.Handler
	boundKeys map[string]struct{}
}

func NewContextHandler(inner slog.Handler) slog.Handler {
	return &ContextHandler{inner: inner, boundKeys: make(map[string]struct{})}
}
func (h *ContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}
func (h *ContextHandler) Handle(ctx context.Context, record slog.Record) error {
	present := make(map[string]bool, len(h.boundKeys)+4)
	for key := range h.boundKeys {
		present[key] = true
	}
	record.Attrs(func(a slog.Attr) bool { present[a.Key] = true; return true })
	if id := TraceID(ctx); id != "" && !present["trace_id"] {
		record.AddAttrs(slog.String("trace_id", id))
	}
	if id := InstallationID(ctx); id != 0 && !present["installation_id"] {
		record.AddAttrs(slog.Int64("installation_id", id))
	}
	if login := GithubLogin(ctx); login != "" && !present["github_login"] {
		record.AddAttrs(slog.String("github_login", login))
	}
	return h.inner.Handle(ctx, record)
}
func (h *ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	bound := make(map[string]struct{}, len(h.boundKeys)+len(attrs))
	for key := range h.boundKeys {
		bound[key] = struct{}{}
	}
	for _, attr := range attrs {
		bound[attr.Key] = struct{}{}
	}
	return &ContextHandler{inner: h.inner.WithAttrs(attrs), boundKeys: bound}
}
func (h *ContextHandler) WithGroup(name string) slog.Handler {
	bound := make(map[string]struct{}, len(h.boundKeys))
	for key := range h.boundKeys {
		bound[key] = struct{}{}
	}
	return &ContextHandler{inner: h.inner.WithGroup(name), boundKeys: bound}
}

// SanitizedHeaders preserves operationally useful HTTP metadata without
// writing reusable credentials or session cookies to Fly's log store.
func SanitizedHeaders(headers http.Header) map[string][]string {
	out := make(map[string][]string, len(headers))
	for key, values := range headers {
		if sensitiveName(key) {
			out[key] = []string{"[REDACTED]"}
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

// SanitizedURL redacts credential-bearing query parameters while retaining the
// route, host, and non-secret query values needed to reproduce a call.
func SanitizedURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clone := *u
	if clone.User != nil {
		clone.User = url.User("[REDACTED]")
	}
	q := clone.Query()
	for key := range q {
		if sensitiveName(key) {
			q.Set(key, "[REDACTED]")
		}
	}
	clone.RawQuery = q.Encode()
	return clone.String()
}

func sensitiveName(name string) bool {
	n := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), " ", "_"))
	switch n {
	case "authorization", "proxy_authorization", "cookie", "set_cookie",
		"password", "passwd", "secret", "client_secret", "webhook_secret",
		"private_key", "api_key", "apikey", "access_token", "refresh_token",
		"id_token", "session_token", "token", "jwt", "sig", "signature",
		"x_hub_signature", "x_hub_signature_256", "x_argus_mermaid_secret",
		"ocp_apim_subscription_key":
		return true
	}
	return strings.HasSuffix(n, "_password") || strings.HasSuffix(n, "_secret") ||
		strings.HasSuffix(n, "_api_key") || strings.HasSuffix(n, "_access_token") ||
		strings.HasSuffix(n, "_refresh_token") || strings.HasSuffix(n, "_private_key") ||
		strings.HasSuffix(n, "_signature") || strings.HasSuffix(n, "_token")
}

// RedactPayload removes only structurally identified credentials from JSON.
// Arbitrary text (prompts, source code, diffs, model output) remains complete.
func RedactPayload(payload []byte) []byte {
	if len(bytes.TrimSpace(payload)) == 0 {
		return payload
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return payload
	}
	redactJSONValue(value)
	redacted, err := json.Marshal(value)
	if err != nil {
		return payload
	}
	return redacted
}

func redactJSONValue(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if sensitiveName(key) {
				v[key] = "[REDACTED]"
				continue
			}
			redactJSONValue(child)
		}
	case []any:
		for _, child := range v {
			redactJSONValue(child)
		}
	}
}

func payloadText(payload []byte) (encoding, text string) {
	if utf8.Valid(payload) {
		return "utf-8", string(payload)
	}
	return "base64", base64.StdEncoding.EncodeToString(payload)
}

// LogPayload writes an entire payload as ordered chunks. The original byte
// count remains visible even when structural credential redaction changes the
// logged representation.
func LogPayload(ctx context.Context, logger *slog.Logger, message, operationID, direction, contentType string, payload []byte) {
	if logger == nil {
		logger = slog.Default()
	}
	originalBytes := len(payload)
	payload = RedactPayload(payload)
	encoding, text := payloadText(payload)
	chunks := splitLogChunks(text, payloadChunkBytes)
	if len(chunks) == 0 {
		chunks = []string{""}
	}
	for i, chunk := range chunks {
		logger.InfoContext(ctx, message,
			"operation_id", operationID,
			"direction", direction,
			"content_type", contentType,
			"encoding", encoding,
			"chunk_index", i+1,
			"chunk_count", len(chunks),
			"payload_bytes", originalBytes,
			"payload", chunk,
		)
	}
}

func splitLogChunks(text string, maxBytes int) []string {
	if text == "" {
		return nil
	}
	chunks := make([]string, 0, len(text)/maxBytes+1)
	for len(text) > maxBytes {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		if cut == 0 {
			cut = maxBytes
		}
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	return append(chunks, text)
}

// PayloadStream converts a stream into chunked payload log records without
// retaining the whole body in memory. Call Finish exactly once.
type PayloadStream struct {
	ctx         context.Context
	logger      *slog.Logger
	message     string
	operationID string
	direction   string
	contentType string

	mu       sync.Mutex
	pending  []byte
	bytes    int64
	chunks   int
	finished bool
}

func NewPayloadStream(ctx context.Context, logger *slog.Logger, message, operationID, direction, contentType string) *PayloadStream {
	if logger == nil {
		logger = slog.Default()
	}
	return &PayloadStream{ctx: ctx, logger: logger, message: message, operationID: operationID, direction: direction, contentType: contentType}
}

func (s *PayloadStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return len(p), nil
	}
	s.bytes += int64(len(p))
	s.pending = append(s.pending, p...)
	for len(s.pending) > payloadChunkBytes {
		cut := payloadChunkBytes
		for cut > 0 && !utf8.RuneStart(s.pending[cut]) {
			cut--
		}
		if cut == 0 {
			cut = payloadChunkBytes
		}
		s.emitLocked(s.pending[:cut])
		s.pending = s.pending[cut:]
	}
	return len(p), nil
}

func (s *PayloadStream) emitLocked(chunk []byte) {
	s.chunks++
	encoding, text := payloadText(chunk)
	s.logger.InfoContext(s.ctx, s.message,
		"operation_id", s.operationID,
		"direction", s.direction,
		"content_type", s.contentType,
		"encoding", encoding,
		"chunk_index", s.chunks,
		"payload", text,
	)
}

func (s *PayloadStream) Finish() (bytesRead int64, chunks int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.finished {
		if len(s.pending) > 0 || s.chunks == 0 {
			s.emitLocked(s.pending)
		}
		s.pending = nil
		s.finished = true
	}
	return s.bytes, s.chunks
}

// LoggingReadCloser logs bytes as its consumer reads them and preserves the
// original close semantics. It does not retain the complete body in memory.
type LoggingReadCloser struct {
	io.ReadCloser
	stream *PayloadStream
}

func NewLoggingReadCloser(body io.ReadCloser, stream *PayloadStream) *LoggingReadCloser {
	return &LoggingReadCloser{ReadCloser: body, stream: stream}
}
func (r *LoggingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		_, _ = r.stream.Write(p[:n])
	}
	return n, err
}
func (r *LoggingReadCloser) Finish() (int64, int) { return r.stream.Finish() }

const maxBufferedCredentialBody = 8 << 20

// BufferedLoggingReadCloser records the bytes consumed by a handler and emits
// them through LogPayload at Finish. Unlike streaming capture, this permits
// structural JSON credential redaction for inbound API bodies.
type BufferedLoggingReadCloser struct {
	io.ReadCloser
	ctx         context.Context
	logger      *slog.Logger
	message     string
	operationID string
	direction   string
	contentType string
	body        bytes.Buffer
	bytesRead   int64
	truncated   bool
	once        sync.Once
	chunks      int
}

func NewBufferedLoggingReadCloser(body io.ReadCloser, ctx context.Context, logger *slog.Logger, message, operationID, direction, contentType string) *BufferedLoggingReadCloser {
	return &BufferedLoggingReadCloser{ReadCloser: body, ctx: ctx, logger: logger, message: message,
		operationID: operationID, direction: direction, contentType: contentType}
}
func (r *BufferedLoggingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.bytesRead += int64(n)
	if n > 0 && r.body.Len() < maxBufferedCredentialBody {
		remaining := maxBufferedCredentialBody - r.body.Len()
		toWrite := min(n, remaining)
		_, _ = r.body.Write(p[:toWrite])
		if toWrite < n {
			r.truncated = true
		}
	} else if n > 0 {
		r.truncated = true
	}
	return n, err
}
func (r *BufferedLoggingReadCloser) Finish() (int64, int) {
	r.once.Do(func() {
		redacted := RedactPayload(r.body.Bytes())
		_, text := payloadText(redacted)
		r.chunks = len(splitLogChunks(text, payloadChunkBytes))
		if r.chunks == 0 {
			r.chunks = 1
		}
		LogPayload(r.ctx, r.logger, r.message, r.operationID, r.direction, r.contentType, r.body.Bytes())
		if r.truncated {
			r.logger.WarnContext(r.ctx, "credential-bearing request body log truncated",
				"operation_id", r.operationID, "captured_bytes", r.body.Len(), "limit_bytes", maxBufferedCredentialBody)
		}
	})
	return r.bytesRead, r.chunks
}

func sanitizedTransportError(err error, requestURL *url.URL) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if requestURL != nil {
		message = strings.ReplaceAll(message, requestURL.String(), SanitizedURL(requestURL))
	}
	return message
}

// LoggingRoundTripper records complete outbound HTTP request and response
// metadata and bodies. It never changes the bytes observed by the server or
// caller. Authentication-bearing fields are structurally redacted.
type requestBodyFinisher interface {
	io.ReadCloser
	Finish() (int64, int)
}

type LoggingRoundTripper struct {
	Base    http.RoundTripper
	Service string
	Logger  *slog.Logger
}

func NewLoggingRoundTripper(service string, base http.RoundTripper) http.RoundTripper {
	return &LoggingRoundTripper{Base: base, Service: service}
}

func (t *LoggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	logger := t.Logger
	if logger == nil {
		logger = slog.Default()
	}
	operationID := NewLogID()
	start := time.Now()
	logger.InfoContext(req.Context(), "outbound HTTP request started",
		"operation_id", operationID,
		"service", t.Service,
		"method", req.Method,
		"url", SanitizedURL(req.URL),
		"headers", SanitizedHeaders(req.Header),
		"content_length", req.ContentLength,
	)
	var requestBody requestBodyFinisher
	if req.Body != nil && req.Body != http.NoBody {
		if t.Service == "posthog" {
			// PostHog places its reusable project key in the JSON body. Buffer it
			// so structural redaction occurs before any bytes reach stdout.
			requestBody = NewBufferedLoggingReadCloser(req.Body, req.Context(), logger,
				"outbound HTTP request body", operationID, "request", req.Header.Get("Content-Type"))
		} else {
			stream := NewPayloadStream(req.Context(), logger, "outbound HTTP request body",
				operationID, "request", req.Header.Get("Content-Type"))
			requestBody = NewLoggingReadCloser(req.Body, stream)
		}
		req.Body = requestBody
	}

	resp, err := base.RoundTrip(req)
	if requestBody != nil {
		bodyBytes, chunks := requestBody.Finish()
		logger.InfoContext(req.Context(), "outbound HTTP request body consumed",
			"operation_id", operationID, "service", t.Service,
			"request_body_bytes", bodyBytes, "request_body_chunks", chunks)
	}
	if err != nil {
		logger.ErrorContext(req.Context(), "outbound HTTP request failed",
			"operation_id", operationID,
			"service", t.Service,
			"duration_ms", time.Since(start).Milliseconds(),
			"error", sanitizedTransportError(err, req.URL),
		)
		return nil, err
	}
	logger.InfoContext(req.Context(), "outbound HTTP response headers",
		"operation_id", operationID,
		"service", t.Service,
		"status_code", resp.StatusCode,
		"headers", SanitizedHeaders(resp.Header),
		"content_length", resp.ContentLength,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	stream := NewPayloadStream(req.Context(), logger, "outbound HTTP response body", operationID, "response", resp.Header.Get("Content-Type"))
	resp.Body = &loggingResponseBody{
		ReadCloser:  resp.Body,
		stream:      stream,
		ctx:         req.Context(),
		logger:      logger,
		service:     t.Service,
		operationID: operationID,
		statusCode:  resp.StatusCode,
		start:       start,
	}
	return resp, nil
}

type loggingResponseBody struct {
	io.ReadCloser
	stream      *PayloadStream
	ctx         context.Context
	logger      *slog.Logger
	service     string
	operationID string
	statusCode  int
	start       time.Time
	once        sync.Once
	closeErr    error
}

func (b *loggingResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		_, _ = b.stream.Write(p[:n])
	}
	return n, err
}
func (b *loggingResponseBody) Close() error {
	b.once.Do(func() {
		b.closeErr = b.ReadCloser.Close()
		bodyBytes, chunks := b.stream.Finish()
		b.logger.InfoContext(b.ctx, "outbound HTTP request completed",
			"operation_id", b.operationID,
			"service", b.service,
			"status_code", b.statusCode,
			"duration_ms", time.Since(b.start).Milliseconds(),
			"response_body_bytes", bodyBytes,
			"response_body_chunks", chunks,
			"close_error", b.closeErr,
		)
	})
	return b.closeErr
}
