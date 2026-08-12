package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/BeLazy167/argus/backend/internal/obs"
	gh "github.com/google/go-github/v68/github"
	"golang.org/x/time/rate"
)

// Client wraps go-github operations needed by the review pipeline.
// All methods are rate-limited to avoid GitHub's secondary rate limits.
type Client struct {
	app *App

	// appSlug is the GitHub App slug this deployment runs as; the App's bot
	// login is "<slug>[bot]". Used to recognize our own reviews/comments.
	appSlug string

	// restLimiter throttles REST API calls (Contents, PullRequests, Issues, etc.).
	// GitHub's secondary rate limit triggers at ~100 requests in a short window.
	// 20 req/s with burst 5 keeps us well under the threshold.
	restLimiter *rate.Limiter

	// searchLimiter throttles Code Search API calls, which have stricter limits
	// (~30 req/min). 1 req/2s with burst 2 keeps us safe.
	searchLimiter *rate.Limiter
}

func NewClient(app *App, appSlug string) *Client {
	return &Client{
		app:           app,
		appSlug:       appSlug,
		restLimiter:   rate.NewLimiter(rate.Limit(20), 5),            // 20 req/s, burst 5
		searchLimiter: rate.NewLimiter(rate.Every(2*time.Second), 2), // 1 req/2s, burst 2
	}
}

// graphQLErrors represents GraphQL-level errors in the response.
type graphQLErrors struct {
	Errors []struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"errors"`
}

// doGraphQL executes a GraphQL request and checks for both HTTP and GraphQL errors.
// It reads the raw response body to detect GraphQL errors that go-github's Do ignores.
func doGraphQL(ctx context.Context, client *gh.Client, body any, result any) error {
	req, err := client.NewRequest("POST", "graphql", body)
	if err != nil {
		return fmt.Errorf("creating graphql request: %w", err)
	}
	// BareDo sends the request without reading/closing the body, so we can read it ourselves.
	// client.Do(ctx, req, nil) reads and closes the body, making subsequent ReadAll fail.
	resp, err := client.BareDo(ctx, req)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading graphql response: %w", err)
	}
	// Check for GraphQL errors
	var gqlErr graphQLErrors
	if json.Unmarshal(raw, &gqlErr) == nil && len(gqlErr.Errors) > 0 {
		msgs := make([]string, len(gqlErr.Errors))
		for i, e := range gqlErr.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("graphql errors: %s", strings.Join(msgs, "; "))
	}
	// Decode the actual result
	if result != nil {
		if err := json.Unmarshal(raw, result); err != nil {
			return fmt.Errorf("decoding graphql result: %w", err)
		}
	}
	return nil
}

// GetPRDiff fetches the unified diff for a pull request.
func (c *Client) GetPRDiff(ctx context.Context, installationID int64, owner, repo string, prNumber int) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}

	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	diff, _, err := client.PullRequests.GetRaw(ctx, owner, repo, prNumber, gh.RawOptions{Type: gh.Diff})
	if err != nil {
		return "", fmt.Errorf("fetching PR diff: %w", err)
	}
	return diff, nil
}

// PRCommit is a trimmed view of a PR commit used by intent extraction.
// Only the fields we actually read are exported; go-github's RepositoryCommit
// carries many more.
type PRCommit struct {
	SHA     string
	Author  string
	Message string // full git commit message (subject + body, newline separated)
}

// ListPRCommits fetches the commits on a pull request and returns the most
// recent maxCommits of them. GitHub's PullRequests.ListCommits paginates
// chronologically (oldest first), so we must walk every page then take the
// tail — breaking early would keep the oldest commits, which is precisely the
// opposite of what intent extraction wants.
//
// Used by intent extraction to pull the author's per-commit narrative when
// the PR description is thin.
func (c *Client) ListPRCommits(ctx context.Context, installationID int64, owner, repo string, prNumber, maxCommits int) ([]PRCommit, error) {
	if maxCommits <= 0 {
		return nil, nil
	}
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}

	var all []PRCommit
	opts := &gh.ListOptions{PerPage: 100}
	for {
		if err := c.restLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}
		commits, resp, err := client.PullRequests.ListCommits(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, fmt.Errorf("listing PR commits: %w", err)
		}
		for _, rc := range commits {
			all = append(all, PRCommit{
				SHA:     rc.GetSHA(),
				Author:  rc.GetCommit().GetAuthor().GetName(),
				Message: rc.GetCommit().GetMessage(),
			})
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	if len(all) > maxCommits {
		all = all[len(all)-maxCommits:]
	}
	return all, nil
}

// GetPRFiles fetches per-file change data for a pull request with pagination.
func (c *Client) GetPRFiles(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]*gh.CommitFile, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}

	var all []*gh.CommitFile
	opts := &gh.ListOptions{PerPage: 100}
	for {
		if err := c.restLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}
		files, resp, err := client.PullRequests.ListFiles(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, fmt.Errorf("listing PR files: %w", err)
		}
		all = append(all, files...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

// UpdatePRDescription updates the body of a pull request.
func (c *Client) UpdatePRDescription(ctx context.Context, installationID int64, owner, repo string, prNumber int, body string) error {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return err
	}

	if err := c.restLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
	if _, _, err = client.PullRequests.Edit(ctx, owner, repo, prNumber, &gh.PullRequest{Body: gh.Ptr(body)}); err != nil {
		return fmt.Errorf("updating PR description: %w", err)
	}
	return nil
}

type permanentFileContentError struct {
	err error
}

func (e *permanentFileContentError) Error() string { return e.err.Error() }
func (e *permanentFileContentError) Unwrap() error { return e.err }

// IsPermanentGitObjectError reports whether retrying the same immutable Git
// object cannot succeed. Authentication, rate limits, transport failures, and
// server errors are deliberately transient.
func IsPermanentGitObjectError(err error) bool {
	var permanent *permanentFileContentError
	if errors.As(err, &permanent) {
		return true
	}
	// go-github rejects literal catch-all directory names such as [...slug]
	// before making a request because they contain "..". The immutable tree
	// cannot change, so retrying that path representation can never succeed.
	if strings.Contains(err.Error(), "path must not contain '..' due to auth vulnerability issue") {
		return true
	}
	var responseErr *gh.ErrorResponse
	if !errors.As(err, &responseErr) || responseErr.Response == nil {
		return false
	}
	switch responseErr.Response.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusGone, http.StatusUnprocessableEntity:
		return true
	default:
		return false
	}
}

// maxFileContentBytes bounds the raw-blob fallback before parser and prompt
// callers receive the content. The Contents API already inlines files up to
// 1 MiB; 5 MiB covers unusually large source files without allowing one blob
// to dominate a graph window's memory.
const maxFileContentBytes = 5 << 20

type rawBlobFetcher func(context.Context, string) ([]byte, error)

func decodeRepositoryContent(ctx context.Context, content *gh.RepositoryContent, fetchRaw rawBlobFetcher) (string, error) {
	decoded, err := content.GetContent()
	if err == nil {
		return decoded, nil
	}
	if content.GetEncoding() != "none" {
		return "", &permanentFileContentError{err: fmt.Errorf("decoding content: %w", err)}
	}
	if content.GetSHA() == "" {
		return "", &permanentFileContentError{err: errors.New("raw blob fallback requires a blob SHA")}
	}
	if content.GetSize() < 0 || content.GetSize() > maxFileContentBytes {
		return "", &permanentFileContentError{err: fmt.Errorf("file size %d exceeds graph source limit %d", content.GetSize(), maxFileContentBytes)}
	}
	raw, err := fetchRaw(ctx, content.GetSHA())
	if err != nil {
		return "", fmt.Errorf("fetching raw blob: %w", err)
	}
	if len(raw) > maxFileContentBytes {
		return "", &permanentFileContentError{err: fmt.Errorf("raw blob size %d exceeds graph source limit %d", len(raw), maxFileContentBytes)}
	}
	return string(raw), nil
}

// GetFileContent fetches the content of a file from a repo at a specific ref.
func (c *Client) GetFileContent(ctx context.Context, installationID int64, owner, repo, path, ref string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}

	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	content, _, _, err := client.Repositories.GetContents(ctx, owner, repo, path, &gh.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		return "", fmt.Errorf("fetching file content: %w", err)
	}
	if content == nil {
		return "", &permanentFileContentError{err: fmt.Errorf("file %s is not a regular file at ref %s", path, ref)}
	}

	return decodeRepositoryContent(ctx, content, func(ctx context.Context, sha string) ([]byte, error) {
		if err := c.restLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}
		raw, _, err := client.Git.GetBlobRaw(ctx, owner, repo, sha)
		return raw, err
	})
}

// reviewPostError carries the only safe automatic-retry fact: no CreateReview
// request could have created a review. Every other failure remains ambiguous.
type reviewPostError struct {
	err                  error
	definitelyNotCreated bool
}

func (e *reviewPostError) Error() string              { return e.err.Error() }
func (e *reviewPostError) Unwrap() error              { return e.err }
func (e *reviewPostError) DefinitelyNotCreated() bool { return e.definitelyNotCreated }

// IsReviewDefinitelyNotCreated reports whether GitHub conclusively rejected the
// mutation, or whether it failed before any CreateReview request was sent.
func IsReviewDefinitelyNotCreated(err error) bool {
	var certain interface{ DefinitelyNotCreated() bool }
	return errors.As(err, &certain) && certain.DefinitelyNotCreated()
}

func definitelyNotCreated(err error) error {
	return &reviewPostError{err: err, definitelyNotCreated: true}
}

func isConclusiveReview4xx(err error) bool {
	var responseErr *gh.ErrorResponse
	return errors.As(err, &responseErr) && responseErr.Response != nil &&
		responseErr.Response.StatusCode >= http.StatusBadRequest && responseErr.Response.StatusCode < http.StatusInternalServerError
}

// PostReview creates a pull request review with all inline comments in one atomic API call.
// Comments must be pre-validated — invalid lines should be folded into the summary body
// by the caller, not included in the Comments slice.
func (c *Client) PostReview(ctx context.Context, installationID int64, owner, repo string, prNumber int, review *ReviewSubmission) (int64, error) {
	if review == nil {
		return 0, definitelyNotCreated(errors.New("posting review: nil submission"))
	}
	if c == nil || c.app == nil {
		return 0, definitelyNotCreated(errors.New("creating github client: GitHub App is not configured"))
	}
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return 0, definitelyNotCreated(fmt.Errorf("creating github client: %w", err))
	}

	comments := make([]*gh.DraftReviewComment, len(review.Comments))
	for i, rc := range review.Comments {
		comments[i] = &gh.DraftReviewComment{
			Path: gh.Ptr(rc.Path),
			Body: gh.Ptr(rc.Body),
			Line: gh.Ptr(rc.Line),
			Side: gh.Ptr(rc.Side),
		}
		if rc.StartLine > 0 {
			comments[i].StartLine = gh.Ptr(rc.StartLine)
			comments[i].StartSide = gh.Ptr(rc.Side)
		}
	}

	req := &gh.PullRequestReviewRequest{
		Body:     gh.Ptr(review.Summary),
		Event:    gh.Ptr("COMMENT"),
		Comments: comments,
	}
	// Pin the review to the exact commit the diff was fetched against.
	// Without this, GitHub resolves line numbers against the current HEAD,
	// which may have changed since we fetched the diff (force-push, new commit).
	// This also prevents "submitted too quickly" errors on synchronize events
	// because GitHub can resolve positions against a known commit SHA.
	if review.HeadSHA != "" {
		req.CommitID = gh.Ptr(review.HeadSHA)
	}

	// Single atomic call. Retry on transient errors. Once any request has an
	// ambiguous outcome, a later conclusive response cannot erase that earlier
	// uncertainty.
	if err := c.restLimiter.Wait(ctx); err != nil {
		return 0, definitelyNotCreated(fmt.Errorf("rate limit wait: %w", err))
	}
	mutationUncertain := false
	createReview := func() (*gh.PullRequestReview, error) {
		created, _, createErr := client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
		if createErr != nil && !isConclusiveReview4xx(createErr) {
			mutationUncertain = true
		}
		return created, createErr
	}
	ghReview, err := createReview()

	// Handle secondary rate limit (403) — respect Retry-After and retry once.
	if err != nil {
		var abuseErr *gh.AbuseRateLimitError
		if errors.As(err, &abuseErr) {
			wait := 60 * time.Second // default if no Retry-After header
			if abuseErr.RetryAfter != nil && *abuseErr.RetryAfter > 0 {
				wait = *abuseErr.RetryAfter
			}
			// Cap the wait to avoid blocking the pipeline too long
			if wait > 2*time.Minute {
				wait = 2 * time.Minute
			}
			slog.Warn("review post hit secondary rate limit, waiting",
				"retry_after", wait, "comments", len(comments), "error", err)
			// github.api.rate_limited captures every abuse-limit trip so
			// PostHog funnels can bucket by endpoint+reset time without
			// regex-parsing log lines. reset_at is ISO-8601 RFC3339 so the
			// dashboard can plot it on a time axis directly.
			slog.WarnContext(ctx, "github api rate limited",
				slog.String("event", "github.api.rate_limited"),
				slog.String("endpoint", "pulls.CreateReview"),
				slog.String("reset_at", time.Now().Add(wait).UTC().Format(time.RFC3339)),
				slog.String("trace_id", obs.TraceID(ctx)),
			)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				waitErr := fmt.Errorf("context cancelled during rate limit wait: %w", ctx.Err())
				if !mutationUncertain {
					return 0, definitelyNotCreated(waitErr)
				}
				return 0, &reviewPostError{err: waitErr}
			}
			ghReview, err = createReview()
		}
	}

	// A 5xx may mean GitHub committed the review and lost the response. Never
	// repeat that mutation: an immediate negative list is not authoritative under
	// eventual consistency. An exact marker match is positive evidence; every
	// other result remains ambiguous for the durable dashboard reconciler.
	if err != nil && isRetryable(err) {
		slog.Warn("review post failed (5xx), reconciling exact marker without retrying mutation",
			"comments", len(comments), "error", err)
		marker := reviewMarkerFromBody(review.Summary)
		if marker != "" {
			existingID, found, checkErr := c.FindReviewByMarker(ctx, installationID, owner, repo, prNumber, marker, review.HeadSHA)
			if checkErr == nil && found {
				slog.Info("review was created despite 5xx, recovered exact marker",
					"github_review_id", existingID)
				return existingID, nil
			}
			slog.Warn("exact review marker not yet observable after 5xx", "check_error", checkErr)
		}
	}
	if err != nil && is422(err) {
		errStr := err.Error()
		if strings.Contains(errStr, "submitted too quickly") {
			// GitHub hasn't computed the diff yet — wait and retry with all comments intact.
			slog.Warn("review post failed (422 submitted too quickly), waiting before retry",
				"comments", len(comments), "error", err)
			time.Sleep(10 * time.Second)
			ghReview, err = createReview()
			// If still failing with position errors after the wait, fall through to start_line stripping.
			if err != nil && is422(err) {
				errStr = err.Error()
				if strings.Contains(errStr, "submitted too quickly") {
					// Second attempt also too quick — wait longer.
					slog.Warn("review post still too quick, waiting longer",
						"comments", len(comments), "error", err)
					time.Sleep(20 * time.Second)
					ghReview, err = createReview()
				}
			}
		}
	}
	if err != nil && is422(err) {
		errStr := err.Error()
		// Only strip start_line if the 422 is about line resolution, not other validation errors
		if strings.Contains(errStr, "pull_request_review_thread") || strings.Contains(errStr, "line") || strings.Contains(errStr, "start_line") || strings.Contains(errStr, "position") {
			slog.Warn("review post failed (422 line resolution), retrying without start_line", "comments", len(comments), "error", err)
			for i := range comments {
				comments[i].StartLine = nil
				comments[i].StartSide = nil
			}
			ghReview, err = createReview()
		} else {
			slog.Warn("review post failed (422 non-line)", "comments", len(comments), "error", err)
		}
	}
	if err != nil {
		postErr := fmt.Errorf("posting review: %w", err)
		if !mutationUncertain && isConclusiveReview4xx(err) {
			return 0, definitelyNotCreated(postErr)
		}
		return 0, &reviewPostError{err: postErr}
	}
	if ghReview == nil || ghReview.GetID() <= 0 {
		return 0, &reviewPostError{err: errors.New("posting review: GitHub returned no review id")}
	}
	return ghReview.GetID(), nil
}

func reviewMarkerFromBody(body string) string {
	const prefix = "<!-- argus-review:"
	start := strings.LastIndex(body, prefix)
	if start < 0 {
		return ""
	}
	end := strings.Index(body[start:], "-->")
	if end < 0 {
		return ""
	}
	return body[start : start+end+len("-->")]
}

// ReviewMarker is the stable, hidden identity embedded in one review attempt.
// It contains no tenant data and is exact-matchable during remote reconciliation.
func ReviewMarker(reviewID string, generation int) string {
	return fmt.Sprintf("<!-- argus-review:%s:generation:%d -->", reviewID, generation)
}

// FindReviewByMarker performs a complete paginated lookup through the same
// installation-scoped client used to post. A match must have the exact marker,
// our bot identity, and (when supplied) the reviewed head commit.
func (c *Client) FindReviewByMarker(ctx context.Context, installationID int64, owner, repo string, prNumber int, marker, headSHA string) (int64, bool, error) {
	if c == nil || c.app == nil {
		return 0, false, errors.New("listing reviews: GitHub App is not configured")
	}
	if marker == "" {
		return 0, false, errors.New("listing reviews: empty reconciliation marker")
	}
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return 0, false, fmt.Errorf("creating github client: %w", err)
	}
	opts := &gh.ListOptions{PerPage: 100}
	for {
		if err := c.restLimiter.Wait(ctx); err != nil {
			return 0, false, fmt.Errorf("rate limit wait: %w", err)
		}
		reviews, resp, err := client.PullRequests.ListReviews(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return 0, false, fmt.Errorf("listing reviews for reconciliation: %w", err)
		}
		for _, review := range reviews {
			if !strings.Contains(review.GetBody(), marker) ||
				!IsArgusThread(review.GetUser().GetLogin(), c.appSlug) ||
				(headSHA != "" && review.GetCommitID() != headSHA) {
				continue
			}
			if review.GetID() <= 0 {
				return 0, false, errors.New("reconciled GitHub review has no id")
			}
			return review.GetID(), true, nil
		}
		if resp == nil || resp.NextPage == 0 {
			return 0, false, nil
		}
		opts.Page = resp.NextPage
	}
}

func isRetryable(err error) bool {
	var ghErr *gh.ErrorResponse
	if errors.As(err, &ghErr) {
		code := ghErr.Response.StatusCode
		return code == 502 || code == 503 || code == 504
	}
	return false
}

func is422(err error) bool {
	var ghErr *gh.ErrorResponse
	if errors.As(err, &ghErr) {
		return ghErr.Response.StatusCode == 422
	}
	return false
}

// GetCompareCommitsDiff fetches the diff between two commits (for incremental re-review).
func (c *Client) GetCompareCommitsDiff(ctx context.Context, installationID int64, owner, repo, base, head string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}

	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}

	comparison, _, err := client.Repositories.CompareCommits(ctx, owner, repo, base, head, nil)
	if err != nil {
		return "", fmt.Errorf("comparing commits: %w", err)
	}

	var sb strings.Builder
	for _, f := range comparison.Files {
		if f.Patch != nil {
			fmt.Fprintf(&sb, "diff --git a/%s b/%s\n", f.GetFilename(), f.GetFilename())
			fmt.Fprintf(&sb, "--- a/%s\n+++ b/%s\n", f.GetPreviousFilename(), f.GetFilename())
			sb.WriteString(f.GetPatch())
			sb.WriteByte('\n')
		}
	}
	return sb.String(), nil
}

// CommitTouch is a commit that modified a specific file: its SHA and the
// author's GitHub login (empty when the commit isn't linked to a GH account).
type CommitTouch struct {
	SHA   string
	Login string
}

// ListCommitsTouchingFile returns commits reachable from ref that modified
// path since the given time, oldest first. One page (100 commits) is plenty
// for the address-detection window between a review comment and the merge.
func (c *Client) ListCommitsTouchingFile(ctx context.Context, installationID int64, owner, repo, path, ref string, since time.Time) ([]CommitTouch, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}
	commits, _, err := client.Repositories.ListCommits(ctx, owner, repo, &gh.CommitsListOptions{
		SHA:         ref,
		Path:        path,
		Since:       since,
		ListOptions: gh.ListOptions{PerPage: 100},
	})
	if err != nil {
		return nil, fmt.Errorf("listing commits for %s: %w", path, err)
	}
	out := make([]CommitTouch, 0, len(commits))
	// GitHub returns newest first; reverse to oldest-first.
	for i := len(commits) - 1; i >= 0; i-- {
		rc := commits[i]
		login := rc.GetAuthor().GetLogin()
		if login == "" {
			login = rc.GetCommitter().GetLogin()
		}
		out = append(out, CommitTouch{SHA: rc.GetSHA(), Login: login})
	}
	return out, nil
}

// ListReviewComments returns all comments for a specific review, used to capture github_comment_ids after posting.
func (c *Client) ListReviewComments(ctx context.Context, installationID int64, owner, repo string, prNumber int, reviewID int64) ([]*gh.PullRequestComment, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}

	var all []*gh.PullRequestComment
	opts := &gh.ListOptions{PerPage: 100}
	for {
		if err := c.restLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}
		comments, resp, err := client.PullRequests.ListReviewComments(ctx, owner, repo, prNumber, reviewID, opts)
		if err != nil {
			return nil, fmt.Errorf("listing review comments: %w", err)
		}
		all = append(all, comments...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

// ReplyToComment posts a reply to an existing PR review comment thread.
func (c *Client) ReplyToComment(ctx context.Context, installationID int64, owner, repo string, prNumber int, commentID int64, body string) (*gh.PullRequestComment, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}

	if err := c.restLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}
	reply, _, err := client.PullRequests.CreateCommentInReplyTo(ctx, owner, repo, prNumber, body, commentID)
	if err != nil {
		return nil, fmt.Errorf("replying to comment: %w", err)
	}
	return reply, nil
}

// GetPullRequest fetches full PR details (for constructing PREvent from issue_comment).
func (c *Client) GetPullRequest(ctx context.Context, installationID int64, owner, repo string, prNumber int) (*PREvent, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}
	pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("fetching PR: %w", err)
	}
	return &PREvent{
		InstallationID: installationID,
		RepoFullName:   owner + "/" + repo,
		PRNumber:       prNumber,
		PRTitle:        pr.GetTitle(),
		PRAuthor:       pr.GetUser().GetLogin(),
		HeadSHA:        pr.GetHead().GetSHA(),
		BaseSHA:        pr.GetBase().GetSHA(),
		BaseRef:        pr.GetBase().GetRef(),
		HeadRef:        pr.GetHead().GetRef(),
		PRBody:         pr.GetBody(),
	}, nil
}

// AddReaction adds an emoji reaction to an issue comment.
func (c *Client) AddReaction(ctx context.Context, installationID int64, owner, repo string, commentID int64, reaction string) error {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
	_, _, err = client.Reactions.CreateIssueCommentReaction(ctx, owner, repo, commentID, reaction)
	return err
}

// ErrReviewCommentNotFound means GitHub authoritatively reported that the referenced
// pull request review comment no longer exists. Callers may treat its reaction
// aggregate as neutral. Other API failures must remain fail-closed.
var ErrReviewCommentNotFound = errors.New("GitHub review comment not found")

type reviewCommentNotFoundError struct {
	commentID int64
	err       error
}

func (e *reviewCommentNotFoundError) Error() string {
	return fmt.Sprintf("listing reactions on pull request review comment %d: %v", e.commentID, e.err)
}
func (e *reviewCommentNotFoundError) Unwrap() error { return e.err }
func (e *reviewCommentNotFoundError) Is(target error) bool {
	return target == ErrReviewCommentNotFound
}

// commentReactionListError translates only an endpoint-level 404 into the
// deleted-comment type. The original GitHub error stays in the chain for
// diagnostics; auth, rate-limit, transport, and server failures stay ordinary
// errors so the mandatory pre-review sweep fails closed.
func commentReactionListError(commentID int64, resp *gh.Response, err error) error {
	status := 0
	if resp != nil && resp.Response != nil {
		status = resp.StatusCode
	}
	if status == 0 {
		var responseErr *gh.ErrorResponse
		if errors.As(err, &responseErr) && responseErr.Response != nil {
			status = responseErr.Response.StatusCode
		}
	}
	if status == http.StatusNotFound {
		return &reviewCommentNotFoundError{commentID: commentID, err: err}
	}
	return fmt.Errorf("listing comment reactions: %w", err)
}

// CommentReaction represents a single reaction on a PR review comment.
type CommentReaction struct {
	ID      int64
	User    string
	Content string // "+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes"
}

// ListCommentReactions fetches all reactions on a pull request review comment.
func (c *Client) ListCommentReactions(ctx context.Context, installationID int64, owner, repo string, commentID int64) ([]CommentReaction, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}

	var all []CommentReaction
	opts := &gh.ListOptions{PerPage: 100}
	for {
		if err := c.restLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}
		reactions, resp, err := client.Reactions.ListPullRequestCommentReactions(ctx, owner, repo, commentID, opts)
		if err != nil {
			return nil, commentReactionListError(commentID, resp, err)
		}
		for _, r := range reactions {
			all = append(all, CommentReaction{
				ID:      r.GetID(),
				User:    r.GetUser().GetLogin(),
				Content: r.GetContent(),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

// CreateIssueComment posts a comment on an issue or PR.
func (c *Client) CreateIssueComment(ctx context.Context, installationID int64, owner, repo string, number int, body string) error {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
	_, _, err = client.Issues.CreateComment(ctx, owner, repo, number, &gh.IssueComment{Body: gh.Ptr(body)})
	return err
}

// UpdateIssueComment edits the body of an existing issue comment by its REST
// comment ID. Used e.g., to swap the "Trigger" checkbox line for a "Running"
// marker once a checkbox-triggered review has been dispatched.
func (c *Client) UpdateIssueComment(ctx context.Context, installationID int64, owner, repo string, commentID int64, body string) error {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
	_, _, err = client.Issues.EditComment(ctx, owner, repo, commentID, &gh.IssueComment{Body: gh.Ptr(body)})
	return err
}

// CreateIssueCommentWithNodeID posts a comment and returns its GraphQL node ID (for minimizing later).
func (c *Client) CreateIssueCommentWithNodeID(ctx context.Context, installationID int64, owner, repo string, number int, body string) (string, error) {
	nodeID, _, err := c.CreateIssueCommentRef(ctx, installationID, owner, repo, number, body)
	return nodeID, err
}

// CreateIssueCommentRef posts a comment and returns BOTH identities GitHub
// assigns it: the GraphQL node id (minimize) and the REST id (edit). They are
// not interchangeable — minimizeComment takes only the former and
// Issues.EditComment only the latter — and a comment that must be rewritten
// later from another process needs the REST id persisted.
func (c *Client) CreateIssueCommentRef(ctx context.Context, installationID int64, owner, repo string, number int, body string) (nodeID string, commentID int64, err error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", 0, err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", 0, fmt.Errorf("rate limit wait: %w", err)
	}
	comment, _, err := client.Issues.CreateComment(ctx, owner, repo, number, &gh.IssueComment{Body: gh.Ptr(body)})
	if err != nil {
		return "", 0, err
	}
	return comment.GetNodeID(), comment.GetID(), nil
}

// HasRepoWriteAccess reports whether login can push to the repo.
//
// This is the authorization check for actions a webhook attributes to a user
// who is NOT the comment author — notably ticking a task-list checkbox in a
// bot-authored comment. author_association on such an event describes the
// COMMENT's author (Argus), not the person who toggled the box, so it cannot
// authorize them; only an explicit permission lookup on the actor can.
//
// "admin", "maintain" and "write" pass; "triage", "read" and "none" do not.
// GitHub returns 404 for a user with no access at all, which is a denial, not
// an error — but every other failure IS returned, so callers can fail closed
// rather than treat an outage as permission.
func (c *Client) HasRepoWriteAccess(ctx context.Context, installationID int64, owner, repo, login string) (bool, error) {
	if login == "" {
		return false, nil
	}
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return false, err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return false, fmt.Errorf("rate limit wait: %w", err)
	}
	perm, resp, err := client.Repositories.GetPermissionLevel(ctx, owner, repo, login)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	switch perm.GetPermission() {
	case "admin", "maintain", "write":
		return true, nil
	default:
		return false, nil
	}
}

// ListPRComments returns ALL review comments on a PR (across all reviews).
func (c *Client) ListPRComments(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]*gh.PullRequestComment, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}

	var all []*gh.PullRequestComment
	opts := &gh.PullRequestListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}
	for {
		if err := c.restLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}
		comments, resp, err := client.PullRequests.ListComments(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, fmt.Errorf("listing PR comments: %w", err)
		}
		all = append(all, comments...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return all, nil
}

// ReviewThread represents a review thread from the GraphQL API.
type ReviewThread struct {
	ID         string
	IsResolved bool
	// First comment in the thread
	AuthorLogin string
	Body        string
	Path        string
	Line        int
	// FirstCommentID is the REST database ID of the thread's first comment
	// (0 when GraphQL omitted it). Lets callers reply on the thread via
	// ReplyToComment before resolving it.
	FirstCommentID int64
}

// ListReviewThreads fetches unresolved review threads via GraphQL.
func (c *Client) ListReviewThreads(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]ReviewThread, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}

	var threads []ReviewThread
	var after *string
	for {
		body := map[string]any{
			"query": `query($owner: String!, $repo: String!, $pr: Int!, $after: String) {
				repository(owner: $owner, name: $repo) {
					pullRequest(number: $pr) {
						reviewThreads(first: 100, after: $after) {
							nodes {
								id
								isResolved
								comments(first: 1) {
									nodes {
										author { login }
										databaseId
										body
										path
										line
									}
								}
							}
							pageInfo { hasNextPage endCursor }
						}
					}
				}
			}`,
			"variables": map[string]any{
				"owner": owner,
				"repo":  repo,
				"pr":    prNumber,
				"after": after,
			},
		}

		var result struct {
			Data struct {
				Repository struct {
					PullRequest struct {
						ReviewThreads struct {
							Nodes []struct {
								ID         string `json:"id"`
								IsResolved bool   `json:"isResolved"`
								Comments   struct {
									Nodes []struct {
										Author struct {
											Login string `json:"login"`
										} `json:"author"`
										DatabaseID int64  `json:"databaseId"`
										Body       string `json:"body"`
										Path       string `json:"path"`
										Line       int    `json:"line"`
									} `json:"nodes"`
								} `json:"comments"`
							} `json:"nodes"`
							PageInfo struct {
								HasNextPage bool   `json:"hasNextPage"`
								EndCursor   string `json:"endCursor"`
							} `json:"pageInfo"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}

		if err := c.restLimiter.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limit wait: %w", err)
		}
		if err := doGraphQL(ctx, client, body, &result); err != nil {
			return nil, fmt.Errorf("graphql reviewThreads: %w", err)
		}

		page := result.Data.Repository.PullRequest.ReviewThreads
		for _, n := range page.Nodes {
			t := ReviewThread{ID: n.ID, IsResolved: n.IsResolved}
			if len(n.Comments.Nodes) > 0 {
				c0 := n.Comments.Nodes[0]
				t.AuthorLogin = c0.Author.Login
				t.FirstCommentID = c0.DatabaseID
				t.Body = c0.Body
				t.Path = c0.Path
				t.Line = c0.Line
			}
			threads = append(threads, t)
		}
		if !page.PageInfo.HasNextPage {
			return threads, nil
		}
		if page.PageInfo.EndCursor == "" {
			return nil, fmt.Errorf("graphql reviewThreads: next page has empty cursor")
		}
		cursor := page.PageInfo.EndCursor
		after = &cursor
	}
}

// ResolveReviewThread marks a review thread as resolved via GraphQL.
func (c *Client) ResolveReviewThread(ctx context.Context, installationID int64, threadID string) error {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return err
	}
	body := map[string]any{
		"query": `mutation($input: ResolveReviewThreadInput!) { resolveReviewThread(input: $input) { thread { isResolved } } }`,
		"variables": map[string]any{
			"input": map[string]string{
				"threadId": threadID,
			},
		},
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
	return doGraphQL(ctx, client, body, nil)
}

// FindThreadForComment returns the thread ID for a given review comment node ID.
func (c *Client) FindThreadForComment(ctx context.Context, installationID int64, owner, repo string, prNumber int, commentNodeID string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}

	body := map[string]any{
		"query": `query($owner: String!, $repo: String!, $pr: Int!) {
			repository(owner: $owner, name: $repo) {
				pullRequest(number: $pr) {
					reviewThreads(first: 100) {
						nodes {
							id
							comments(first: 50) {
								nodes { id }
							}
						}
					}
				}
			}
		}`,
		"variables": map[string]any{"owner": owner, "repo": repo, "pr": prNumber},
	}

	var result struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							ID       string `json:"id"`
							Comments struct {
								Nodes []struct {
									ID string `json:"id"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}

	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	if err := doGraphQL(ctx, client, body, &result); err != nil {
		return "", err
	}

	for _, t := range result.Data.Repository.PullRequest.ReviewThreads.Nodes {
		for _, c := range t.Comments.Nodes {
			if c.ID == commentNodeID {
				return t.ID, nil
			}
		}
	}
	return "", fmt.Errorf("thread not found for comment %s", commentNodeID)
}

// MinimizeComment hides a comment via GraphQL minimizeComment mutation.
func (c *Client) MinimizeComment(ctx context.Context, installationID int64, nodeID, reason string) error {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return err
	}

	body := map[string]any{
		"query": `mutation($input: MinimizeCommentInput!) { minimizeComment(input: $input) { minimizedComment { isMinimized } } }`,
		"variables": map[string]any{
			"input": map[string]string{
				"subjectId":  nodeID,
				"classifier": reason,
			},
		},
	}

	if err := c.restLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
	return doGraphQL(ctx, client, body, nil)
}

// --- Git Data API (for @argus-eye fix command) ---

// CreateBlob creates a blob in the repo and returns its SHA.
func (c *Client) CreateBlob(ctx context.Context, installationID int64, owner, repo, content, encoding string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	blob, _, err := client.Git.CreateBlob(ctx, owner, repo, &gh.Blob{
		Content:  gh.Ptr(content),
		Encoding: gh.Ptr(encoding),
	})
	if err != nil {
		return "", fmt.Errorf("creating blob: %w", err)
	}
	return blob.GetSHA(), nil
}

// CreateTree creates a tree object from entries and returns its SHA.
func (c *Client) CreateTree(ctx context.Context, installationID int64, owner, repo, baseTreeSHA string, entries []*gh.TreeEntry) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	tree, _, err := client.Git.CreateTree(ctx, owner, repo, baseTreeSHA, entries)
	if err != nil {
		return "", fmt.Errorf("creating tree: %w", err)
	}
	return tree.GetSHA(), nil
}

// CreateCommit creates a commit object and returns its SHA.
func (c *Client) CreateCommit(ctx context.Context, installationID int64, owner, repo, message, treeSHA string, parentSHAs []string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}
	parents := make([]*gh.Commit, len(parentSHAs))
	for i, sha := range parentSHAs {
		parents[i] = &gh.Commit{SHA: gh.Ptr(sha)}
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	commit, _, err := client.Git.CreateCommit(ctx, owner, repo, &gh.Commit{
		Message: gh.Ptr(message),
		Tree:    &gh.Tree{SHA: gh.Ptr(treeSHA)},
		Parents: parents,
	}, nil)
	if err != nil {
		return "", fmt.Errorf("creating commit: %w", err)
	}
	return commit.GetSHA(), nil
}

// UpdateRef updates a git reference to point to a new SHA.
func (c *Client) UpdateRef(ctx context.Context, installationID int64, owner, repo, ref, sha string) error {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait: %w", err)
	}
	_, _, err = client.Git.UpdateRef(ctx, owner, repo, &gh.Reference{
		Ref:    gh.Ptr(ref),
		Object: &gh.GitObject{SHA: gh.Ptr(sha)},
	}, false)
	if err != nil {
		return fmt.Errorf("updating ref: %w", err)
	}
	return nil
}

// GetRef returns the SHA a ref points to.
func (c *Client) GetRef(ctx context.Context, installationID int64, owner, repo, ref string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	r, _, err := client.Git.GetRef(ctx, owner, repo, ref)
	if err != nil {
		return "", fmt.Errorf("getting ref: %w", err)
	}
	return r.GetObject().GetSHA(), nil
}

// GetCommitTree returns the tree SHA for a given commit.
func (c *Client) GetCommitTree(ctx context.Context, installationID int64, owner, repo, commitSHA string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	commit, _, err := client.Git.GetCommit(ctx, owner, repo, commitSHA)
	if err != nil {
		return "", fmt.Errorf("getting commit: %w", err)
	}
	return commit.GetTree().GetSHA(), nil
}

// SearchCode searches for a symbol name in a repository and returns matching file paths.
// Uses the GitHub code search API. Returns up to 5 unique file paths.
func (c *Client) SearchCode(ctx context.Context, installationID int64, owner, repo, query string) ([]string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf("%s repo:%s/%s", query, owner, repo)
	if err := c.searchLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}
	result, _, err := client.Search.Code(ctx, q, &gh.SearchOptions{ListOptions: gh.ListOptions{PerPage: 10}})
	if err != nil {
		return nil, fmt.Errorf("code search: %w", err)
	}
	seen := make(map[string]bool)
	var paths []string
	for _, r := range result.CodeResults {
		path := r.GetPath()
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
		if len(paths) >= 5 {
			break
		}
	}
	return paths, nil
}

// RepoTree is a recursive Git Trees response. Truncated means GitHub omitted
// entries and callers must not treat Files as an authoritative repository view.
type RepoTree struct {
	Files     []RepoTreeFile
	Truncated bool
}

// RepoTreeFile identifies one immutable blob returned by a recursive tree.
// Size is used to reject oversized source files before spending a blob fetch.
type RepoTreeFile struct {
	Path string
	SHA  string
	Mode string
	Size int64
}

// RepositoryMetadata is the repository identity and default branch needed to
// arbitrate a branch-name mismatch from an out-of-order push webhook.
type RepositoryMetadata struct {
	ID            int64
	FullName      string
	DefaultBranch string
}

// GetRepositoryMetadata reads current repository authority with the target
// installation's GitHub client. It deliberately returns only fields needed by
// webhook arbitration.
func (c *Client) GetRepositoryMetadata(ctx context.Context, installationID int64, owner, repo string) (RepositoryMetadata, error) {
	if c.app == nil {
		return RepositoryMetadata{}, errors.New("github app is unavailable")
	}
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return RepositoryMetadata{}, err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return RepositoryMetadata{}, fmt.Errorf("rate limit wait: %w", err)
	}
	repository, _, err := client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return RepositoryMetadata{}, fmt.Errorf("fetching repository metadata: %w", err)
	}
	return RepositoryMetadata{
		ID:            repository.GetID(),
		FullName:      repository.GetFullName(),
		DefaultBranch: repository.GetDefaultBranch(),
	}, nil
}

// ResolveDefaultBranchCommit resolves a mutable branch name once. The returned
// SHA is then used for both the tree and every file fetch in a full index.
func (c *Client) ResolveDefaultBranchCommit(ctx context.Context, installationID int64, owner, repo, branch string) (string, error) {
	return c.GetRef(ctx, installationID, owner, repo, "heads/"+strings.TrimPrefix(branch, "refs/heads/"))
}

// GetRepoTree returns all immutable file blobs at a commit SHA.
func (c *Client) GetRepoTree(ctx context.Context, installationID int64, owner, repo, commitSHA string) (RepoTree, error) {
	// The Git Trees endpoint is keyed by a tree object, not a commit object.
	// Resolve the commit's root tree explicitly rather than relying on GitHub to
	// accept a commit SHA as an undocumented shorthand.
	treeSHA, err := c.GetCommitTree(ctx, installationID, owner, repo, commitSHA)
	if err != nil {
		return RepoTree{}, fmt.Errorf("resolving commit tree: %w", err)
	}
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return RepoTree{}, err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return RepoTree{}, fmt.Errorf("rate limit wait: %w", err)
	}
	tree, _, err := client.Git.GetTree(ctx, owner, repo, treeSHA, true)
	if err != nil {
		return RepoTree{}, fmt.Errorf("fetching repo tree: %w", err)
	}
	result := RepoTree{Truncated: tree.GetTruncated()}
	for _, entry := range tree.Entries {
		if entry.GetType() == "blob" {
			result.Files = append(result.Files, RepoTreeFile{
				Path: entry.GetPath(),
				SHA:  entry.GetSHA(),
				Mode: entry.GetMode(),
				Size: int64(entry.GetSize()),
			})
		}
	}
	return result, nil
}

// GetBlobContent fetches one immutable tree blob in a single GitHub call.
func (c *Client) GetBlobContent(ctx context.Context, installationID int64, owner, repo string, file RepoTreeFile) (string, error) {
	if file.SHA == "" {
		return "", &permanentFileContentError{err: fmt.Errorf("file %s has no blob SHA", file.Path)}
	}
	if file.Size < 0 || file.Size > maxFileContentBytes {
		return "", &permanentFileContentError{err: fmt.Errorf("file size %d exceeds graph source limit %d", file.Size, maxFileContentBytes)}
	}
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	raw, _, err := client.Git.GetBlobRaw(ctx, owner, repo, file.SHA)
	if err != nil {
		return "", fmt.Errorf("fetching raw blob: %w", err)
	}
	if len(raw) > maxFileContentBytes {
		return "", &permanentFileContentError{err: fmt.Errorf("raw blob size %d exceeds graph source limit %d", len(raw), maxFileContentBytes)}
	}
	return string(raw), nil
}

// ReviewSubmission represents a formatted review ready to post to GitHub.
type ReviewSubmission struct {
	Summary  string
	HeadSHA  string
	Comments []ReviewComment
}

// ReviewComment is a single inline comment on a PR.
// All comments must have valid line numbers within the diff.
// Invalid-line comments should be folded into the review summary by the caller.
type ReviewComment struct {
	Path      string
	Body      string
	Line      int
	StartLine int    // 0 if single-line comment
	Side      string // "RIGHT" for additions, "LEFT" for deletions
}

// Issue is a trimmed view of a GitHub issue used by the acceptance worker.
// Only the fields we actually read are exported; the full go-github Issue
// type carries many more.
type Issue struct {
	Owner  string
	Repo   string
	Number int
	URL    string
	Title  string
	Body   string
	State  string // "open" | "closed"
}

// GetIssue fetches a single issue's title + body via the REST API.
// Returns an *Issue (or error) for the given owner/repo/number. Used by the
// acceptance worker to pull criteria from issue descriptions.
func (c *Client) GetIssue(ctx context.Context, installationID int64, owner, repo string, number int) (*Issue, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}
	issue, _, err := client.Issues.Get(ctx, owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("fetching issue %s/%s#%d: %w", owner, repo, number, err)
	}
	return &Issue{
		Owner:  owner,
		Repo:   repo,
		Number: number,
		URL:    issue.GetHTMLURL(),
		Title:  issue.GetTitle(),
		Body:   issue.GetBody(),
		State:  issue.GetState(),
	}, nil
}

// ClosingIssueRef is a single issue returned by GitHub's
// closingIssuesReferences GraphQL field. This is GitHub's authoritative
// answer to "what issues does this PR close" — covers PR body text, the
// "Development" UI panel, and branch-name patterns.
type ClosingIssueRef struct {
	Owner  string
	Repo   string
	Number int
	URL    string
	Title  string
	Body   string
}

// GetClosingIssues runs the closingIssuesReferences GraphQL query and returns
// issues GitHub's own parser resolved for this PR. Primary source of issue
// linkage for the acceptance worker — regex fallback only catches non-closing
// mentions ("refs #N") that GraphQL won't return.
//
// Caps at 50 closing issues per PR (first: 50 in the query). PRs that close
// more than 50 issues are extremely rare; if they exist, we return the first
// 50 GitHub serves. Pagination via pageInfo.endCursor is left as a future
// upgrade when a real workload needs it.
func (c *Client) GetClosingIssues(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]ClosingIssueRef, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}

	query := `query($owner: String!, $repo: String!, $number: Int!) {
		repository(owner: $owner, name: $repo) {
			pullRequest(number: $number) {
				closingIssuesReferences(first: 50) {
					nodes {
						number
						title
						body
						url
						repository { nameWithOwner }
					}
				}
			}
		}
	}`

	body := map[string]any{
		"query": query,
		"variables": map[string]any{
			"owner":  owner,
			"repo":   repo,
			"number": prNumber,
		},
	}

	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ClosingIssuesReferences struct {
						Nodes []struct {
							Number     int    `json:"number"`
							Title      string `json:"title"`
							Body       string `json:"body"`
							URL        string `json:"url"`
							Repository struct {
								NameWithOwner string `json:"nameWithOwner"`
							} `json:"repository"`
						} `json:"nodes"`
					} `json:"closingIssuesReferences"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}

	if err := doGraphQL(ctx, client, body, &resp); err != nil {
		return nil, fmt.Errorf("closing issues graphql: %w", err)
	}

	nodes := resp.Data.Repository.PullRequest.ClosingIssuesReferences.Nodes
	out := make([]ClosingIssueRef, 0, len(nodes))
	for _, n := range nodes {
		// Split nameWithOwner into owner/repo
		nOwner, nRepo := owner, repo
		if nwo := n.Repository.NameWithOwner; nwo != "" {
			if i := strings.Index(nwo, "/"); i > 0 {
				nOwner, nRepo = nwo[:i], nwo[i+1:]
			}
		}
		out = append(out, ClosingIssueRef{
			Owner:  nOwner,
			Repo:   nRepo,
			Number: n.Number,
			URL:    n.URL,
			Title:  n.Title,
			Body:   n.Body,
		})
	}
	return out, nil
}
