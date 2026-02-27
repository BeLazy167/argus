package github

import (
	"context"
	"fmt"

	gh "github.com/google/go-github/v68/github"
)

// Client wraps go-github operations needed by the review pipeline.
type Client struct {
	app *App
}

func NewClient(app *App) *Client {
	return &Client{app: app}
}

// GetPRDiff fetches the unified diff for a pull request.
func (c *Client) GetPRDiff(ctx context.Context, installationID int64, owner, repo string, prNumber int) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}

	diff, _, err := client.PullRequests.GetRaw(ctx, owner, repo, prNumber, gh.RawOptions{Type: gh.Diff})
	if err != nil {
		return "", fmt.Errorf("fetching PR diff: %w", err)
	}
	return diff, nil
}

// GetFileContent fetches the content of a file from a repo at a specific ref.
func (c *Client) GetFileContent(ctx context.Context, installationID int64, owner, repo, path, ref string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
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

// PostReview creates a pull request review with inline comments.
func (c *Client) PostReview(ctx context.Context, installationID int64, owner, repo string, prNumber int, review *ReviewSubmission) (int64, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return 0, err
	}

	comments := make([]*gh.DraftReviewComment, len(review.Comments))
	for i, comment := range review.Comments {
		comments[i] = &gh.DraftReviewComment{
			Path: gh.Ptr(comment.Path),
			Body: gh.Ptr(comment.Body),
			Line: gh.Ptr(comment.Line),
			Side: gh.Ptr(comment.Side),
		}
		if comment.StartLine > 0 {
			comments[i].StartLine = gh.Ptr(comment.StartLine)
			comments[i].StartSide = gh.Ptr(comment.Side)
		}
	}

	ghReview, _, err := client.PullRequests.CreateReview(ctx, owner, repo, prNumber, &gh.PullRequestReviewRequest{
		Body:     gh.Ptr(review.Summary),
		Event:    gh.Ptr("COMMENT"),
		Comments: comments,
	})
	if err != nil {
		return 0, fmt.Errorf("posting review: %w", err)
	}
	return ghReview.GetID(), nil
}

// GetCompareCommitsDiff fetches the diff between two commits (for incremental re-review).
func (c *Client) GetCompareCommitsDiff(ctx context.Context, installationID int64, owner, repo, base, head string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
	}

	comparison, _, err := client.Repositories.CompareCommits(ctx, owner, repo, base, head, nil)
	if err != nil {
		return "", fmt.Errorf("comparing commits: %w", err)
	}

	// Build unified diff from file patches
	var diffStr string
	for _, f := range comparison.Files {
		if f.Patch != nil {
			diffStr += fmt.Sprintf("diff --git a/%s b/%s\n", f.GetFilename(), f.GetFilename())
			diffStr += fmt.Sprintf("--- a/%s\n+++ b/%s\n", f.GetPreviousFilename(), f.GetFilename())
			diffStr += f.GetPatch() + "\n"
		}
	}
	return diffStr, nil
}

// ReviewSubmission represents a formatted review ready to post to GitHub.
type ReviewSubmission struct {
	Summary  string
	Comments []ReviewComment
}

// ReviewComment is a single inline comment on a PR.
type ReviewComment struct {
	Path      string
	Body      string
	Line      int
	StartLine int // 0 if single-line comment
	Side      string // "RIGHT" for additions, "LEFT" for deletions
}
