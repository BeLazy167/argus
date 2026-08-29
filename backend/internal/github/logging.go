package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
	gh "github.com/google/go-github/v68/github"
)

// semanticLog records one package-level GitHub operation. Transport logging
// captures bytes on the wire; these records explain why the call was made and
// tie it back to the installation and repository resource being changed.
type semanticLog struct {
	ctx            context.Context
	operation      string
	installationID int64
	repo           string
	prNumber       int
	started        time.Time
	base           []any
}

func beginSemantic(ctx context.Context, operation string, installationID int64, owner, repo string, prNumber int, attrs ...any) *semanticLog {
	if ctx == nil {
		ctx = context.Background()
	}
	if installationID != 0 {
		ctx = obs.SetInstallationID(ctx, installationID)
	}
	repoFullName := repo
	if owner != "" && repo != "" {
		repoFullName = owner + "/" + repo
	}
	l := &semanticLog{ctx: ctx, operation: operation, installationID: installationID, repo: repoFullName, prNumber: prNumber, started: time.Now(), base: attrs}
	slog.InfoContext(ctx, "github semantic operation started", l.args("phase", "start")...)
	return l
}

func (l *semanticLog) args(extra ...any) []any {
	args := []any{
		"component", "github",
		"operation", l.operation,
		"installation_id", l.installationID,
		"repo", l.repo,
		"pr_number", l.prNumber,
	}
	args = append(args, l.base...)
	return append(args, extra...)
}

func (l *semanticLog) finish(err error, result ...any) {
	args := l.args("phase", "finish", "duration_ms", time.Since(l.started).Milliseconds())
	args = append(args, result...)
	if err != nil {
		args = append(args, "outcome", "failure", "error", err)
		slog.ErrorContext(l.ctx, "github semantic operation failed", args...)
		return
	}
	args = append(args, "outcome", "success")
	slog.InfoContext(l.ctx, "github semantic operation succeeded", args...)
}

func semanticResultAttrs(results ...any) []any {
	attrs := make([]any, 0, len(results)*2)
	for i, result := range results {
		value := reflect.ValueOf(result)
		if value.IsValid() && (value.Kind() == reflect.Slice || value.Kind() == reflect.Array || value.Kind() == reflect.Map) {
			attrs = append(attrs, fmt.Sprintf("result_%d_count", i), value.Len())
			continue
		}
		attrs = append(attrs, fmt.Sprintf("result_%d", i), result)
	}
	return attrs
}

// apiCallLog records the semantic GitHub endpoint around each go-github REST
// or GraphQL invocation. It deliberately includes response and rate-limit
// metadata even when values are unavailable, making absence explicit.
type apiCallLog struct {
	*semanticLog
	callID string
}

func beginAPICall(ctx context.Context, operation string, installationID int64, owner, repo string, prNumber int, attrs ...any) *apiCallLog {
	l := beginSemantic(ctx, operation, installationID, owner, repo, prNumber, attrs...)
	l.base = append(l.base, "api", true)
	return &apiCallLog{semanticLog: l, callID: obs.NewLogID()}
}

func (l *apiCallLog) finish(resp *gh.Response, err error, result ...any) {
	statusCode, rateLimit, rateRemaining, rateReset := responseMetadata(resp, err)
	result = append(result,
		"call_id", l.callID,
		"status_code", statusCode,
		"rate_limit", rateLimit,
		"rate_remaining", rateRemaining,
		"rate_reset", rateReset,
	)
	l.semanticLog.finish(err, result...)
}

func responseMetadata(resp *gh.Response, err error) (statusCode, limit, remaining int, reset string) {
	if resp == nil {
		var responseErr *gh.ErrorResponse
		if errors.As(err, &responseErr) && responseErr.Response != nil {
			resp = &gh.Response{Response: responseErr.Response}
		}
	}
	if resp == nil {
		return 0, 0, 0, ""
	}
	if resp.Response != nil {
		statusCode = resp.StatusCode
		if limit == 0 {
			limit = headerInt(resp.Header, "X-RateLimit-Limit")
		}
		if remaining == 0 {
			remaining = headerInt(resp.Header, "X-RateLimit-Remaining")
		}
		reset = resp.Header.Get("X-RateLimit-Reset")
	}
	if resp.Rate.Limit != 0 {
		limit = resp.Rate.Limit
	}
	// Remaining may legitimately be zero, so take it whenever a parsed limit
	// proves the Rate value came from headers.
	if resp.Rate.Limit != 0 {
		remaining = resp.Rate.Remaining
	}
	if !resp.Rate.Reset.Time.IsZero() {
		reset = resp.Rate.Reset.Time.UTC().Format(time.RFC3339)
	}
	return
}

func headerInt(h http.Header, key string) int {
	var n int
	_, _ = fmt.Sscanf(h.Get(key), "%d", &n)
	return n
}

func graphQLOperation(body any) (operation string, owner string, repo string, prNumber int, attrs []any) {
	operation = "graphql.unknown"
	m, ok := body.(map[string]any)
	if !ok {
		return
	}
	query, _ := m["query"].(string)
	switch {
	case strings.Contains(query, "resolveReviewThread"):
		operation = "graphql.resolveReviewThread"
	case strings.Contains(query, "minimizeComment"):
		operation = "graphql.minimizeComment"
	case strings.Contains(query, "closingIssuesReferences"):
		operation = "graphql.closingIssuesReferences"
	case strings.Contains(query, "reviewThreads"):
		operation = "graphql.reviewThreads"
	}
	variables, _ := m["variables"].(map[string]any)
	owner, _ = variables["owner"].(string)
	repo, _ = variables["repo"].(string)
	prNumber = intVariable(variables, "pr")
	if prNumber == 0 {
		prNumber = intVariable(variables, "number")
	}
	if input, ok := variables["input"].(map[string]string); ok {
		for _, key := range []string{"threadId", "subjectId"} {
			if value := input[key]; value != "" {
				attrs = append(attrs, key, value)
			}
		}
	}
	return
}

func intVariable(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}
