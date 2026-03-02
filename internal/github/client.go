package github

import (
	"context"
	"fmt"
	"strings"

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
	}, nil
}

// AddReaction adds an emoji reaction to an issue comment.
func (c *Client) AddReaction(ctx context.Context, installationID int64, owner, repo string, commentID int64, reaction string) error {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return err
	}
	_, _, err = client.Reactions.CreateIssueCommentReaction(ctx, owner, repo, commentID, reaction)
	return err
}

// CreateIssueComment posts a comment on an issue or PR.
func (c *Client) CreateIssueComment(ctx context.Context, installationID int64, owner, repo string, number int, body string) error {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return err
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

	req, err := client.NewRequest("POST", "graphql", body)
	if err != nil {
		return nil, fmt.Errorf("creating graphql request: %w", err)
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

	_, err = client.Do(ctx, req, &result)
	if err != nil {
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

	req, err := client.NewRequest("POST", "graphql", body)
	if err != nil {
		return fmt.Errorf("creating graphql request: %w", err)
	}
	_, err = client.Do(ctx, req, nil)
	return err
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

	req, err := client.NewRequest("POST", "graphql", body)
	if err != nil {
		return "", err
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

	_, err = client.Do(ctx, req, &result)
	if err != nil {
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

	req, err := client.NewRequest("POST", "graphql", body)
	if err != nil {
		return fmt.Errorf("creating graphql request: %w", err)
	}
	_, err = client.Do(ctx, req, nil)
	return err
}

// --- Git Data API (for @argus-eye fix command) ---

// CreateBlob creates a blob in the repo and returns its SHA.
func (c *Client) CreateBlob(ctx context.Context, installationID int64, owner, repo, content, encoding string) (string, error) {
	client, err := c.app.ClientForInstallation(installationID)
	if err != nil {
		return "", err
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
	commit, _, err := client.Git.GetCommit(ctx, owner, repo, commitSHA)
	if err != nil {
		return "", fmt.Errorf("getting commit: %w", err)
	}
	return commit.GetTree().GetSHA(), nil
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
