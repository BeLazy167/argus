package api

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/BeLazy167/argus/backend/internal/store"
)

const (
	listReviewsDefaultLimit = 20
	listReviewsMaxLimit     = 100
)

// reviewSidecarSource is the narrow seam for get_review's four degradable
// reads — the ones whose failure is reported in degraded_sections rather than
// failing the call. It exists so a test can fail exactly one of them; nothing
// else needs it, so it carries nothing else. Same shape as indexerSource: the
// production value is *store.Store, set in NewServer.
type reviewSidecarSource interface {
	ListPRReviewSummaries(ctx context.Context, repoID int64, prNumber int) ([]store.PRReviewSummary, error)
	ListPRAutoResolveEvents(ctx context.Context, repoID int64, prNumber int) ([]store.AutoResolveSummary, error)
	ListReviewMemories(ctx context.Context, installationID int64, reviewID uuid.UUID, limit int) ([]store.LearnedMemory, error)
	CountReviewMemoriesByType(ctx context.Context, installationID int64, reviewID uuid.UUID) ([]store.LearnedMemoryCount, error)
}

// reviewSidecars falls back to the store so a bare &Server{store: …} test
// literal behaves exactly as production does.
func (s *Server) reviewSidecars() reviewSidecarSource {
	if s.reviewSidecarStore != nil {
		return s.reviewSidecarStore
	}
	return s.store
}

var errReviewIDFormat = errors.New("review_id must be a UUID")

// githubPRURL matches the dashboard's builder: the PR page, anchored to the
// Argus review when one was posted.
func githubPRURL(fullName string, pr int, githubReviewID *int64) string {
	base := "https://github.com/" + fullName + "/pull/" + strconv.Itoa(pr)
	if githubReviewID != nil {
		return base + "#pullrequestreview-" + strconv.FormatInt(*githubReviewID, 10)
	}
	return base
}

// scopedReview is the authorization step for every review-taking tool: load
// the review, then GetRepoScoped on its repo — the ONLY tenant check, exactly
// as handlers_reviews.go getReview does. GetReview itself is unscoped.
func (t *mcpTools) scopedReview(ctx context.Context, rawID string) (*store.Review, *store.Repo, error) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return nil, nil, errReviewIDFormat
	}
	review, err := t.srv.store.GetReview(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errNotAccessible
		}
		return nil, nil, err
	}
	repo, err := t.srv.store.GetRepoScoped(ctx, review.RepoID, t.scope.installationIDs)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errNotAccessible
		}
		return nil, nil, err
	}
	return review, repo, nil
}

// reviewToolErr maps scopedReview's outcomes: caller-facing errors pass
// through, anything else is logged and replaced by the fixed failure string.
func (t *mcpTools) reviewToolErr(ctx context.Context, tool string, err error) error {
	if errors.Is(err, errNotAccessible) || errors.Is(err, errReviewIDFormat) {
		return err
	}
	return t.internalErr(ctx, tool, err)
}

func rfc3339(ts time.Time) string { return ts.UTC().Format(time.RFC3339) }

func rfc3339Ptr(ts *time.Time) *string {
	if ts == nil {
		return nil
	}
	s := rfc3339(*ts)
	return &s
}

type listReviewsInput struct {
	RepoID int64 `json:"repo_id,omitempty" jsonschema:"local repo id; omit for every repo the caller can access"`
	Limit  int   `json:"limit,omitempty" jsonschema:"default 20, max 100"`
	Offset int   `json:"offset,omitempty"`
}

type reviewSummary struct {
	ReviewID      string  `json:"review_id"`
	RepoID        int64   `json:"repo_id"`
	PRNumber      int     `json:"pr_number"`
	PRTitle       string  `json:"pr_title"`
	Status        string  `json:"status" jsonschema:"pending, in_progress, completed, failed, or cancelled"`
	Score         *int    `json:"score,omitempty"`
	DeepReview    bool    `json:"deep_review"`
	IsIncremental bool    `json:"is_incremental"`
	CreatedAt     string  `json:"created_at"`
	CompletedAt   *string `json:"completed_at,omitempty"`
}

type listReviewsOutput struct {
	Reviews []reviewSummary `json:"reviews"`
}

// listReviews projects only summary fields. List rows are not detail rows —
// token_usage, brief and review_contract are absent and diagrams is a
// synthesized "[]" — so returning store.Review here would look complete and
// not be.
func (t *mcpTools) listReviews(ctx context.Context, _ *mcp.CallToolRequest, in listReviewsInput) (*mcp.CallToolResult, listReviewsOutput, error) {
	var zero listReviewsOutput
	if err := t.requireScope(scopeRead); err != nil {
		return nil, zero, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = listReviewsDefaultLimit
	}
	if limit > listReviewsMaxLimit {
		limit = listReviewsMaxLimit
	}
	offset := max(in.Offset, 0)

	var reviews []store.Review
	var err error
	if in.RepoID != 0 {
		if _, _, scopeErr := t.scopedRepo(ctx, in.RepoID); scopeErr != nil {
			if errors.Is(scopeErr, errNotAccessible) {
				return nil, zero, scopeErr
			}
			return nil, zero, t.internalErr(ctx, "list_reviews", scopeErr)
		}
		reviews, err = t.srv.store.ListReviewsScoped(ctx, in.RepoID, t.scope.installationIDs, limit, offset)
	} else {
		reviews, err = t.srv.store.ListAllReviewsScoped(ctx, t.scope.installationIDs, limit, offset)
	}
	if err != nil {
		return nil, zero, t.internalErr(ctx, "list_reviews", err)
	}
	out := listReviewsOutput{Reviews: make([]reviewSummary, 0, len(reviews))}
	for _, r := range reviews {
		out.Reviews = append(out.Reviews, reviewSummary{
			ReviewID: r.ID.String(), RepoID: r.RepoID, PRNumber: r.PRNumber, PRTitle: r.PRTitle, Status: r.Status, Score: r.Score,
			DeepReview: r.DeepReview, IsIncremental: r.IsIncremental, CreatedAt: rfc3339(r.CreatedAt), CompletedAt: rfc3339Ptr(r.CompletedAt),
		})
	}
	return nil, out, nil
}

type getReviewStatusInput struct {
	ReviewID string `json:"review_id" jsonschema:"review UUID from list_reviews or the dashboard URL"`
}

type getReviewStatusOutput struct {
	ReviewID          string  `json:"review_id"`
	Status            string  `json:"status" jsonschema:"pending, in_progress, completed, failed, or cancelled"`
	Stage             string  `json:"stage,omitempty" jsonschema:"pipeline stage of the current run (triaging, reviewing, scoring, synthesizing, posting, ...); omitted when no run exists yet"`
	AttemptGeneration int     `json:"attempt_generation"`
	Score             *int    `json:"score,omitempty"`
	Error             *string `json:"error,omitempty"`
	DurationMs        *int    `json:"duration_ms,omitempty"`
	CreatedAt         string  `json:"created_at"`
	CompletedAt       *string `json:"completed_at,omitempty"`
	RepoFullName      string  `json:"repo_full_name"`
	PRNumber          int     `json:"pr_number"`
	GitHubPRURL       string  `json:"github_pr_url"`
}

// getReviewStatus is the cheap poll. Stage comes from pipeline_states and is
// never derived from status: the vocabularies differ (5 values vs 13).
func (t *mcpTools) getReviewStatus(ctx context.Context, _ *mcp.CallToolRequest, in getReviewStatusInput) (*mcp.CallToolResult, getReviewStatusOutput, error) {
	var zero getReviewStatusOutput
	if err := t.requireScope(scopeRead); err != nil {
		return nil, zero, err
	}
	review, repo, err := t.scopedReview(ctx, in.ReviewID)
	if err != nil {
		return nil, zero, t.reviewToolErr(ctx, "get_review_status", err)
	}
	gen, err := t.srv.store.GetReviewAttemptGeneration(ctx, review.ID)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review_status attempt generation", "error", err, "review_id", review.ID)
	}
	stage := ""
	if st, err := t.srv.store.GetLatestRunStateForReview(ctx, review.ID); err == nil {
		stage = st
	} else if !errors.Is(err, pgx.ErrNoRows) {
		t.srv.logger.WarnContext(ctx, "get_review_status run state", "error", err, "review_id", review.ID)
	}
	return nil, getReviewStatusOutput{
		ReviewID: review.ID.String(), Status: review.Status, Stage: stage, AttemptGeneration: gen,
		Score: review.Score, Error: review.Error, DurationMs: review.DurationMs,
		CreatedAt: rfc3339(review.CreatedAt), CompletedAt: rfc3339Ptr(review.CompletedAt),
		RepoFullName: repo.FullName, PRNumber: review.PRNumber,
		GitHubPRURL: githubPRURL(repo.FullName, review.PRNumber, review.GithubReviewID),
	}, nil
}

type getReviewInput struct {
	ReviewID          string `json:"review_id" jsonschema:"review UUID from list_reviews or the dashboard URL"`
	IncludeSuppressed bool   `json:"include_suppressed,omitempty" jsonschema:"also return findings that were suppressed and never posted to the PR"`
}

type findingCounts struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	ByFile     map[string]int `json:"by_file"`
}

// getReviewOutput embeds store types (uuid.UUID, json.RawMessage), so the tool
// is registered with Out = any to skip output-schema inference. Content is
// still the JSON of this struct.
type getReviewOutput struct {
	Review             *store.Review              `json:"review"`
	RepoFullName       string                     `json:"repo_full_name"`
	GitHubPRURL        string                     `json:"github_pr_url"`
	AttemptGeneration  int                        `json:"attempt_generation"`
	Findings           []store.ReviewComment      `json:"findings"`
	SuppressedFindings []store.ReviewComment      `json:"suppressed_findings,omitempty"`
	MinorNotes         []store.ReviewMinorNote    `json:"minor_notes"`
	History            []store.PRReviewSummary    `json:"history"`
	AutoResolveEvents  []store.AutoResolveSummary `json:"auto_resolve_events"`
	Memories           []store.LearnedMemory      `json:"memories"`
	MemoryCounts       []store.LearnedMemoryCount `json:"memory_counts"`
	Counts             findingCounts              `json:"counts"`
	DegradedSections   []string                   `json:"degraded_sections"`
}

// getReview returns what the dashboard review page renders, in one call, with
// three deliberate differences from the REST handler it mirrors
// (handlers_reviews.go getReview): repo_full_name and the PR URL come from the
// authz check instead of a second round-trip; the four sidecars that degrade
// to nil on error are NAMED in degraded_sections instead of serializing as an
// indistinguishable null; and suppressed findings are partitioned before
// counting so counts describe what the caller received.
func (t *mcpTools) getReview(ctx context.Context, _ *mcp.CallToolRequest, in getReviewInput) (*mcp.CallToolResult, any, error) {
	if err := t.requireScope(scopeRead); err != nil {
		return nil, nil, err
	}
	review, repo, err := t.scopedReview(ctx, in.ReviewID)
	if err != nil {
		return nil, nil, t.reviewToolErr(ctx, "get_review", err)
	}
	comments, err := t.srv.store.GetReviewComments(ctx, review.ID)
	if err != nil {
		return nil, nil, t.internalErr(ctx, "get_review", err)
	}
	minorNotes, err := t.srv.store.GetReviewMinorNotes(ctx, review.ID)
	if err != nil {
		return nil, nil, t.internalErr(ctx, "get_review", err)
	}
	gen, err := t.srv.store.GetReviewAttemptGeneration(ctx, review.ID)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review attempt generation", "error", err, "review_id", review.ID)
	}

	degraded := []string{}
	sidecars := t.srv.reviewSidecars()
	history, err := sidecars.ListPRReviewSummaries(ctx, review.RepoID, review.PRNumber)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review history", "error", err, "review_id", review.ID)
		degraded, history = append(degraded, "history"), nil
	}
	autoResolves, err := sidecars.ListPRAutoResolveEvents(ctx, review.RepoID, review.PRNumber)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review auto-resolve events", "error", err, "review_id", review.ID)
		degraded, autoResolves = append(degraded, "auto_resolve_events"), nil
	}
	// Memory reads are scoped by the repo's installation — the tenant that
	// granted access — never by the request.
	memories, err := sidecars.ListReviewMemories(ctx, repo.InstallationID, review.ID, 0)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review memories", "error", err, "review_id", review.ID)
		degraded, memories = append(degraded, "memories"), nil
	}
	memoryCounts, err := sidecars.CountReviewMemoriesByType(ctx, repo.InstallationID, review.ID)
	if err != nil {
		t.srv.logger.WarnContext(ctx, "get_review memory counts", "error", err, "review_id", review.ID)
		degraded, memoryCounts = append(degraded, "memory_counts"), nil
	}

	findings := make([]store.ReviewComment, 0, len(comments))
	var suppressed []store.ReviewComment
	counts := findingCounts{BySeverity: map[string]int{}, ByFile: map[string]int{}}
	for _, c := range comments {
		if c.State == "suppressed" {
			if in.IncludeSuppressed {
				suppressed = append(suppressed, c)
			}
			continue
		}
		findings = append(findings, c)
		counts.Total++
		if c.Severity != nil {
			counts.BySeverity[*c.Severity]++
		}
		counts.ByFile[c.FilePath]++
	}
	return nil, getReviewOutput{
		Review: review, RepoFullName: repo.FullName,
		GitHubPRURL:       githubPRURL(repo.FullName, review.PRNumber, review.GithubReviewID),
		AttemptGeneration: gen, Findings: findings, SuppressedFindings: suppressed,
		MinorNotes: nonNil(minorNotes), History: nonNil(history), AutoResolveEvents: nonNil(autoResolves),
		Memories: nonNil(memories), MemoryCounts: nonNil(memoryCounts), Counts: counts, DegradedSections: degraded,
	}, nil
}

// nonNil turns a nil slice into an empty one so JSON carries [] not null:
// null is what a degraded section used to look like, and it is now named in
// degraded_sections instead.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
