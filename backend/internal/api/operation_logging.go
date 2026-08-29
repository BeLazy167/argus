package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
)

// apiOperation provides a consistent correlation envelope for API work. It is
// intentionally independent from HTTP request logging: commands and detached
// webhook work use the same operation/action vocabulary.
type apiOperation struct {
	ctx       context.Context
	logger    *slog.Logger
	id        string
	operation string
	started   time.Time
}

func (s *Server) beginOperation(ctx context.Context, operation string, attrs ...any) *apiOperation {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	op := &apiOperation{ctx: ctx, logger: logger, id: obs.NewLogID(), operation: operation, started: time.Now()}
	args := []any{"operation_id", op.id, "operation", operation, "action", "start"}
	op.logger.InfoContext(ctx, "API operation started", append(args, attrs...)...)
	return op
}

func (op *apiOperation) Finish(w http.ResponseWriter) {
	status := http.StatusOK
	if sw, ok := w.(interface{ Status() int }); ok && sw.Status() != 0 {
		status = sw.Status()
	}
	action := "success"
	level := slog.LevelInfo
	if status >= 500 {
		action, level = "failure", slog.LevelError
	} else if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests {
		action, level = "denial", slog.LevelWarn
	} else if status >= 400 {
		action, level = "rejected", slog.LevelWarn
	}
	op.logger.Log(op.ctx, level, "API operation finished",
		"operation_id", op.id, "operation", op.operation, "action", action,
		"status_code", status, "duration_ms", time.Since(op.started).Milliseconds())
}

func (op *apiOperation) Complete(attrs ...any) {
	args := []any{"operation_id", op.id, "operation", op.operation, "action", "success", "duration_ms", time.Since(op.started).Milliseconds()}
	op.logger.InfoContext(op.ctx, "API operation completed", append(args, attrs...)...)
}

func (op *apiOperation) Outcome(action, message string, attrs ...any) {
	args := []any{"operation_id", op.id, "operation", op.operation, "action", action, "duration_ms", time.Since(op.started).Milliseconds()}
	op.logger.InfoContext(op.ctx, message, append(args, attrs...)...)
}

func (op *apiOperation) Denied(reason string, attrs ...any) {
	args := []any{"operation_id", op.id, "operation", op.operation, "action", "denial", "reason", reason, "duration_ms", time.Since(op.started).Milliseconds()}
	op.logger.WarnContext(op.ctx, "API operation denied", append(args, attrs...)...)
}

func (op *apiOperation) Failed(err error, attrs ...any) {
	args := []any{"operation_id", op.id, "operation", op.operation, "action", "failure", "duration_ms", time.Since(op.started).Milliseconds(), "error", err}
	op.logger.ErrorContext(op.ctx, "API operation failed", append(args, attrs...)...)
}

func (op *apiOperation) Payload(message, direction, contentType string, payload []byte) {
	obs.LogPayload(op.ctx, op.logger, message, op.id, direction, contentType, payload)
}
