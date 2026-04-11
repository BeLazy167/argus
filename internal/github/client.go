package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	gh "github.com/google/go-github/v68/github"
	"golang.org/x/time/rate"
)

// Client wraps go-github operations needed by the review pipeline.
// All methods are rate-limited to avoid GitHub's secondary rate limits.
type Client struct {
	app *App

	// restLimiter throttles REST API calls (Contents, PullRequests, Issues, etc.).
	// GitHub's secondary rate limit triggers at ~100 requests in a short window.
	// 20 req/s with burst 5 keeps us well under the threshold.
	restLimiter *rate.Limiter

	// searchLimiter throttles Code Search API calls, which have stricter limits
	// (~30 req/min). 1 req/2s with burst 2 keeps us safe.
	searchLimiter *rate.Limiter
}

func NewClient(app *App) *Client {
	return &Client{
		app:           app,
		restLimiter:   rate.NewLimiter(rate.Limit(20), 5),  // 20 req/s, burst 5
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
		return "", fmt.Errorf("file %s not found at ref %s", path, ref)
	}

	decoded, err := content.GetContent()
	if err != nil {
		return "", fmt.Errorf("decoding content: %w", err)
	}
	return decoded, nil
}

// PostReview creates a pull request review with all inline comments in one atomic API call.
// Comments must be pre-validated — invalid lines should be folded into the summary body
// by the caller, not included in the Comments slice.
func (c *Client) PostReview(ctx context.Context, installationID int64, owner, repo string, prNumber int, review *ReviewSubmission) (int64, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return 0, fmt.Errorf("creating github client: %w", err)
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

	// Single atomic call. Retry on transient errors.
	if err := c.restLimiter.Wait(ctx); err != nil {
		return 0, fmt.Errorf("rate limit wait: %w", err)
	}
	ghReview, _, err := client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)

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
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return 0, fmt.Errorf("context cancelled during rate limit wait: %w", ctx.Err())
			}
			ghReview, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
		}
	}

	// Retry once on 5xx (transient GitHub errors).
	if err != nil && isRetryable(err) {
		slog.Warn("review post failed (5xx), checking if review was created anyway",
			"comments", len(comments), "error", err)
		time.Sleep(2 * time.Second)

		// GitHub 502s are phantom failures — the review may have been created
		// despite the error response. Check before retrying to avoid duplicates.
		existingID, checkErr := findBotReview(ctx, client, owner, repo, prNumber)
		if checkErr == nil && existingID > 0 {
			slog.Info("review was created despite 5xx, skipping retry",
				"github_review_id", existingID)
			return existingID, nil
		}

		slog.Warn("no existing review found, retrying", "check_error", checkErr)
		ghReview, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
	}
	if err != nil && is422(err) {
		errStr := err.Error()
		if strings.Contains(errStr, "submitted too quickly") {
			// GitHub hasn't computed the diff yet — wait and retry with all comments intact.
			slog.Warn("review post failed (422 submitted too quickly), waiting before retry",
				"comments", len(comments), "error", err)
			time.Sleep(10 * time.Second)
			ghReview, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
			// If still failing with position errors after the wait, fall through to start_line stripping.
			if err != nil && is422(err) {
				errStr = err.Error()
				if strings.Contains(errStr, "submitted too quickly") {
					// Second attempt also too quick — wait longer.
					slog.Warn("review post still too quick, waiting longer",
						"comments", len(comments), "error", err)
					time.Sleep(20 * time.Second)
					ghReview, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
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
			ghReview, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, req)
		} else {
			slog.Warn("review post failed (422 non-line)", "comments", len(comments), "error", err)
		}
	}
	if err != nil {
		return 0, fmt.Errorf("posting review: %w", err)
	}
	return ghReview.GetID(), nil
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

// findBotReview checks if argus-eye[bot] already has a review on this PR
// created in the last 5 minutes. Handles GitHub phantom 502s where the review
// was created server-side but the response was lost.
func findBotReview(ctx context.Context, client *gh.Client, owner, repo string, prNumber int) (int64, error) {
	reviews, _, err := client.PullRequests.ListReviews(ctx, owner, repo, prNumber, &gh.ListOptions{PerPage: 30})
	if err != nil {
		return 0, fmt.Errorf("listing reviews: %w", err)
	}
	cutoff := time.Now().Add(-5 * time.Minute)
	for i := len(reviews) - 1; i >= 0; i-- {
		r := reviews[i]
		login := r.GetUser().GetLogin()
		if (login == "argus-eye[bot]" || login == "argus-eye") && r.GetSubmittedAt().Time.After(cutoff) {
			return r.GetID(), nil
		}
	}
	return 0, nil
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
			return nil, fmt.Errorf("listing comment reactions: %w", err)
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

// CreateIssueCommentWithNodeID posts a comment and returns its GraphQL node ID (for minimizing later).
func (c *Client) CreateIssueCommentWithNodeID(ctx context.Context, installationID int64, owner, repo string, number int, body string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return "", fmt.Errorf("rate limit wait: %w", err)
	}
	comment, _, err := client.Issues.CreateComment(ctx, owner, repo, number, &gh.IssueComment{Body: gh.Ptr(body)})
	if err != nil {
		return "", err
	}
	return comment.GetNodeID(), nil
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
}

// ListReviewThreads fetches unresolved review threads via GraphQL.
func (c *Client) ListReviewThreads(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]ReviewThread, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"query": `query($owner: String!, $repo: String!, $pr: Int!) {
			repository(owner: $owner, name: $repo) {
				pullRequest(number: $pr) {
					reviewThreads(first: 100) {
						nodes {
							id
							isResolved
							comments(first: 1) {
								nodes {
									author { login }
									body
									path
									line
								}
							}
						}
					}
				}
			}
		}`,
		"variables": map[string]any{
			"owner": owner,
			"repo":  repo,
			"pr":    prNumber,
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
									Body string `json:"body"`
									Path string `json:"path"`
									Line int    `json:"line"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
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

	var threads []ReviewThread
	for _, n := range result.Data.Repository.PullRequest.ReviewThreads.Nodes {
		t := ReviewThread{ID: n.ID, IsResolved: n.IsResolved}
		if len(n.Comments.Nodes) > 0 {
			c0 := n.Comments.Nodes[0]
			t.AuthorLogin = c0.Author.Login
			t.Body = c0.Body
			t.Path = c0.Path
			t.Line = c0.Line
		}
		threads = append(threads, t)
	}
	return threads, nil
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

// GetRepoTree returns all file paths in a repo at a given ref using the Git Trees API (recursive).
func (c *Client) GetRepoTree(ctx context.Context, installationID int64, owner, repo, ref string) ([]string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return nil, err
	}
	if err := c.restLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limit wait: %w", err)
	}
	tree, _, err := client.Git.GetTree(ctx, owner, repo, ref, true)
	if err != nil {
		return nil, fmt.Errorf("fetching repo tree: %w", err)
	}
	var paths []string
	for _, entry := range tree.Entries {
		if entry.GetType() == "blob" {
			paths = append(paths, entry.GetPath())
		}
	}
	return paths, nil
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
