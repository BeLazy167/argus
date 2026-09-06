package api

import (
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

type requestBodyLogger interface {
	io.ReadCloser
	Finish() (int64, int)
}

// panicRecovery keeps panic diagnostics in the same NDJSON stream as every
// other backend log. http.ErrAbortHandler retains its standard abort semantics.
func (s *Server) panicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				operationID := obs.NewLogID()
				obs.LogPayload(r.Context(), s.logger, "HTTP panic stack", operationID, "stack", "text/plain", debug.Stack())
				s.logger.ErrorContext(r.Context(), "HTTP handler panic",
					"operation_id", operationID, "panic", fmt.Sprint(recovered),
					"method", r.Method, "url", obs.SanitizedURL(r.URL))
				if r.Header.Get("Connection") != "Upgrade" {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// skipBodyLogging names the paths whose request AND response bodies must not
// reach the payload log. /webhooks/github was excluded on the request side
// already. /mcp carries memory content and review findings derived from
// private source code in both directions — including 401 challenges and tool
// error bodies — and the previous unconditional response tee would have
// written customer code to Fly logs on the first call.
func skipBodyLogging(path string) bool {
	return path == "/webhooks/github" || path == "/mcp" || strings.HasPrefix(path, "/mcp/")
}

// requestLogging records every inbound request and response as structured JSON
// on stdout. Bodies are streamed into small records so large webhooks and
// exports do not create one Fly-truncated line or a second in-memory copy.
func (s *Server) requestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		operationID := obs.NewLogID()
		started := time.Now()
		s.logger.InfoContext(r.Context(), "inbound HTTP request started",
			"operation_id", operationID,
			"method", r.Method,
			"url", obs.SanitizedURL(r.URL),
			"host", r.Host,
			"remote_addr", r.RemoteAddr,
			"headers", obs.SanitizedHeaders(r.Header),
			"content_length", r.ContentLength,
		)

		var requestBody requestBodyLogger
		if r.Body != nil && r.Body != http.NoBody && r.ContentLength != 0 && !skipBodyLogging(r.URL.Path) {
			if strings.Contains(r.URL.Path, "/provider-keys") {
				// This endpoint carries usable provider credentials. Buffer its small
				// JSON body so structural redaction happens before the log write.
				requestBody = obs.NewBufferedLoggingReadCloser(r.Body, r.Context(), s.logger,
					"inbound HTTP request body", operationID, "request", r.Header.Get("Content-Type"))
			} else {
				stream := obs.NewPayloadStream(r.Context(), s.logger, "inbound HTTP request body",
					operationID, "request", r.Header.Get("Content-Type"))
				requestBody = obs.NewLoggingReadCloser(r.Body, stream)
			}
			r.Body = requestBody
		}

		var responseBody *obs.PayloadStream
		ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		if !skipBodyLogging(r.URL.Path) {
			responseBody = obs.NewPayloadStream(r.Context(), s.logger, "inbound HTTP response body", operationID, "response", "")
			ww.Tee(responseBody)
		}

		next.ServeHTTP(ww, r)

		requestBytes, requestChunks := int64(0), 0
		if requestBody != nil {
			requestBytes, requestChunks = requestBody.Finish()
		}
		responseBytes, responseChunks := int64(0), 0
		if responseBody != nil {
			responseBytes, responseChunks = responseBody.Finish()
		}
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		route := ""
		if routeCtx := chi.RouteContext(r.Context()); routeCtx != nil {
			route = routeCtx.RoutePattern()
		}
		s.logger.InfoContext(r.Context(), "inbound HTTP request completed",
			"operation_id", operationID,
			"method", r.Method,
			"url", obs.SanitizedURL(r.URL),
			"route", route,
			"status_code", status,
			"duration_ms", time.Since(started).Milliseconds(),
			"request_body_bytes", requestBytes,
			"request_body_chunks", requestChunks,
			"response_body_bytes", responseBytes,
			"response_body_chunks", responseChunks,
			"response_headers", obs.SanitizedHeaders(ww.Header()),
		)
	})
}
