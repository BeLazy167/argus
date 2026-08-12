package github

import (
	"context"
	"time"

	gh "github.com/google/go-github/v68/github"
)

// GetPRDiff logs and executes the GitHub semantic operation.
func (c *Client) GetPRDiff(ctx context.Context, installationID int64, owner string, repo string, prNumber int) (result0 string, err error) {
	op := beginSemantic(ctx, "client.GetPRDiff", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getPRDiffLogged(ctx, installationID, owner, repo, prNumber)
	return
}

// ListPRCommits logs and executes the GitHub semantic operation.
func (c *Client) ListPRCommits(ctx context.Context, installationID int64, owner string, repo string, prNumber int, maxCommits int) (result0 []PRCommit, err error) {
	op := beginSemantic(ctx, "client.ListPRCommits", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.listPRCommitsLogged(ctx, installationID, owner, repo, prNumber, maxCommits)
	return
}

// GetPRFiles logs and executes the GitHub semantic operation.
func (c *Client) GetPRFiles(ctx context.Context, installationID int64, owner string, repo string, prNumber int) (result0 []*gh.CommitFile, err error) {
	op := beginSemantic(ctx, "client.GetPRFiles", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getPRFilesLogged(ctx, installationID, owner, repo, prNumber)
	return
}

// UpdatePRDescription logs and executes the GitHub semantic operation.
func (c *Client) UpdatePRDescription(ctx context.Context, installationID int64, owner string, repo string, prNumber int, body string) (err error) {
	op := beginSemantic(ctx, "client.UpdatePRDescription", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs()...) }()
	err = c.updatePRDescriptionLogged(ctx, installationID, owner, repo, prNumber, body)
	return
}

// GetFileContent logs and executes the GitHub semantic operation.
func (c *Client) GetFileContent(ctx context.Context, installationID int64, owner string, repo string, path string, ref string) (result0 string, err error) {
	op := beginSemantic(ctx, "client.GetFileContent", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getFileContentLogged(ctx, installationID, owner, repo, path, ref)
	return
}

// PostReview logs and executes the GitHub semantic operation.
func (c *Client) PostReview(ctx context.Context, installationID int64, owner string, repo string, prNumber int, review *ReviewSubmission) (result0 int64, err error) {
	op := beginSemantic(ctx, "client.PostReview", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.postReviewLogged(ctx, installationID, owner, repo, prNumber, review)
	return
}

// FindReviewByMarker logs and executes the GitHub semantic operation.
func (c *Client) FindReviewByMarker(ctx context.Context, installationID int64, owner string, repo string, prNumber int, marker string, headSHA string) (result0 int64, result1 bool, err error) {
	op := beginSemantic(ctx, "client.FindReviewByMarker", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0, result1)...) }()
	result0, result1, err = c.findReviewByMarkerLogged(ctx, installationID, owner, repo, prNumber, marker, headSHA)
	return
}

// GetCompareCommitsDiff logs and executes the GitHub semantic operation.
func (c *Client) GetCompareCommitsDiff(ctx context.Context, installationID int64, owner string, repo string, base string, head string) (result0 string, err error) {
	op := beginSemantic(ctx, "client.GetCompareCommitsDiff", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getCompareCommitsDiffLogged(ctx, installationID, owner, repo, base, head)
	return
}

// ListCommitsTouchingFile logs and executes the GitHub semantic operation.
func (c *Client) ListCommitsTouchingFile(ctx context.Context, installationID int64, owner string, repo string, path string, ref string, since time.Time) (result0 []CommitTouch, err error) {
	op := beginSemantic(ctx, "client.ListCommitsTouchingFile", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.listCommitsTouchingFileLogged(ctx, installationID, owner, repo, path, ref, since)
	return
}

// ListReviewComments logs and executes the GitHub semantic operation.
func (c *Client) ListReviewComments(ctx context.Context, installationID int64, owner string, repo string, prNumber int, reviewID int64) (result0 []*gh.PullRequestComment, err error) {
	op := beginSemantic(ctx, "client.ListReviewComments", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.listReviewCommentsLogged(ctx, installationID, owner, repo, prNumber, reviewID)
	return
}

// ReplyToComment logs and executes the GitHub semantic operation.
func (c *Client) ReplyToComment(ctx context.Context, installationID int64, owner string, repo string, prNumber int, commentID int64, body string) (result0 *gh.PullRequestComment, err error) {
	op := beginSemantic(ctx, "client.ReplyToComment", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.replyToCommentLogged(ctx, installationID, owner, repo, prNumber, commentID, body)
	return
}

// GetPullRequest logs and executes the GitHub semantic operation.
func (c *Client) GetPullRequest(ctx context.Context, installationID int64, owner string, repo string, prNumber int) (result0 *PREvent, err error) {
	op := beginSemantic(ctx, "client.GetPullRequest", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getPullRequestLogged(ctx, installationID, owner, repo, prNumber)
	return
}

// AddReaction logs and executes the GitHub semantic operation.
func (c *Client) AddReaction(ctx context.Context, installationID int64, owner string, repo string, commentID int64, reaction string) (err error) {
	op := beginSemantic(ctx, "client.AddReaction", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs()...) }()
	err = c.addReactionLogged(ctx, installationID, owner, repo, commentID, reaction)
	return
}

// ListCommentReactions logs and executes the GitHub semantic operation.
func (c *Client) ListCommentReactions(ctx context.Context, installationID int64, owner string, repo string, commentID int64) (result0 []CommentReaction, err error) {
	op := beginSemantic(ctx, "client.ListCommentReactions", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.listCommentReactionsLogged(ctx, installationID, owner, repo, commentID)
	return
}

// CreateIssueComment logs and executes the GitHub semantic operation.
func (c *Client) CreateIssueComment(ctx context.Context, installationID int64, owner string, repo string, number int, body string) (err error) {
	op := beginSemantic(ctx, "client.CreateIssueComment", installationID, owner, repo, number)
	defer func() { op.finish(err, semanticResultAttrs()...) }()
	err = c.createIssueCommentLogged(ctx, installationID, owner, repo, number, body)
	return
}

// UpdateIssueComment logs and executes the GitHub semantic operation.
func (c *Client) UpdateIssueComment(ctx context.Context, installationID int64, owner string, repo string, commentID int64, body string) (err error) {
	op := beginSemantic(ctx, "client.UpdateIssueComment", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs()...) }()
	err = c.updateIssueCommentLogged(ctx, installationID, owner, repo, commentID, body)
	return
}

// CreateIssueCommentWithNodeID logs and executes the GitHub semantic operation.
func (c *Client) CreateIssueCommentWithNodeID(ctx context.Context, installationID int64, owner string, repo string, number int, body string) (result0 string, err error) {
	op := beginSemantic(ctx, "client.CreateIssueCommentWithNodeID", installationID, owner, repo, number)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.createIssueCommentWithNodeIDLogged(ctx, installationID, owner, repo, number, body)
	return
}

// CreateIssueCommentRef logs and executes the GitHub semantic operation.
func (c *Client) CreateIssueCommentRef(ctx context.Context, installationID int64, owner string, repo string, number int, body string) (result0 string, result1 int64, err error) {
	op := beginSemantic(ctx, "client.CreateIssueCommentRef", installationID, owner, repo, number)
	defer func() { op.finish(err, semanticResultAttrs(result0, result1)...) }()
	result0, result1, err = c.createIssueCommentRefLogged(ctx, installationID, owner, repo, number, body)
	return
}

// HasRepoWriteAccess logs and executes the GitHub semantic operation.
func (c *Client) HasRepoWriteAccess(ctx context.Context, installationID int64, owner string, repo string, login string) (result0 bool, err error) {
	op := beginSemantic(ctx, "client.HasRepoWriteAccess", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.hasRepoWriteAccessLogged(ctx, installationID, owner, repo, login)
	return
}

// ListPRComments logs and executes the GitHub semantic operation.
func (c *Client) ListPRComments(ctx context.Context, installationID int64, owner string, repo string, prNumber int) (result0 []*gh.PullRequestComment, err error) {
	op := beginSemantic(ctx, "client.ListPRComments", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.listPRCommentsLogged(ctx, installationID, owner, repo, prNumber)
	return
}

// ListReviewThreads logs and executes the GitHub semantic operation.
func (c *Client) ListReviewThreads(ctx context.Context, installationID int64, owner string, repo string, prNumber int) (result0 []ReviewThread, err error) {
	op := beginSemantic(ctx, "client.ListReviewThreads", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.listReviewThreadsLogged(ctx, installationID, owner, repo, prNumber)
	return
}

// ResolveReviewThread logs and executes the GitHub semantic operation.
func (c *Client) ResolveReviewThread(ctx context.Context, installationID int64, threadID string) (err error) {
	op := beginSemantic(ctx, "client.ResolveReviewThread", installationID, "", "", 0)
	defer func() { op.finish(err, semanticResultAttrs()...) }()
	err = c.resolveReviewThreadLogged(ctx, installationID, threadID)
	return
}

// FindThreadForComment logs and executes the GitHub semantic operation.
func (c *Client) FindThreadForComment(ctx context.Context, installationID int64, owner string, repo string, prNumber int, commentNodeID string) (result0 string, err error) {
	op := beginSemantic(ctx, "client.FindThreadForComment", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.findThreadForCommentLogged(ctx, installationID, owner, repo, prNumber, commentNodeID)
	return
}

// MinimizeComment logs and executes the GitHub semantic operation.
func (c *Client) MinimizeComment(ctx context.Context, installationID int64, nodeID string, reason string) (err error) {
	op := beginSemantic(ctx, "client.MinimizeComment", installationID, "", "", 0)
	defer func() { op.finish(err, semanticResultAttrs()...) }()
	err = c.minimizeCommentLogged(ctx, installationID, nodeID, reason)
	return
}

// CreateBlob logs and executes the GitHub semantic operation.
func (c *Client) CreateBlob(ctx context.Context, installationID int64, owner string, repo string, content string, encoding string) (result0 string, err error) {
	op := beginSemantic(ctx, "client.CreateBlob", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.createBlobLogged(ctx, installationID, owner, repo, content, encoding)
	return
}

// CreateTree logs and executes the GitHub semantic operation.
func (c *Client) CreateTree(ctx context.Context, installationID int64, owner string, repo string, baseTreeSHA string, entries []*gh.TreeEntry) (result0 string, err error) {
	op := beginSemantic(ctx, "client.CreateTree", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.createTreeLogged(ctx, installationID, owner, repo, baseTreeSHA, entries)
	return
}

// CreateCommit logs and executes the GitHub semantic operation.
func (c *Client) CreateCommit(ctx context.Context, installationID int64, owner string, repo string, message string, treeSHA string, parentSHAs []string) (result0 string, err error) {
	op := beginSemantic(ctx, "client.CreateCommit", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.createCommitLogged(ctx, installationID, owner, repo, message, treeSHA, parentSHAs)
	return
}

// UpdateRef logs and executes the GitHub semantic operation.
func (c *Client) UpdateRef(ctx context.Context, installationID int64, owner string, repo string, ref string, sha string) (err error) {
	op := beginSemantic(ctx, "client.UpdateRef", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs()...) }()
	err = c.updateRefLogged(ctx, installationID, owner, repo, ref, sha)
	return
}

// GetRef logs and executes the GitHub semantic operation.
func (c *Client) GetRef(ctx context.Context, installationID int64, owner string, repo string, ref string) (result0 string, err error) {
	op := beginSemantic(ctx, "client.GetRef", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getRefLogged(ctx, installationID, owner, repo, ref)
	return
}

// GetCommitTree logs and executes the GitHub semantic operation.
func (c *Client) GetCommitTree(ctx context.Context, installationID int64, owner string, repo string, commitSHA string) (result0 string, err error) {
	op := beginSemantic(ctx, "client.GetCommitTree", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getCommitTreeLogged(ctx, installationID, owner, repo, commitSHA)
	return
}

// SearchCode logs and executes the GitHub semantic operation.
func (c *Client) SearchCode(ctx context.Context, installationID int64, owner string, repo string, query string) (result0 []string, err error) {
	op := beginSemantic(ctx, "client.SearchCode", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.searchCodeLogged(ctx, installationID, owner, repo, query)
	return
}

// GetRepositoryMetadata logs and executes the GitHub semantic operation.
func (c *Client) GetRepositoryMetadata(ctx context.Context, installationID int64, owner string, repo string) (result0 RepositoryMetadata, err error) {
	op := beginSemantic(ctx, "client.GetRepositoryMetadata", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getRepositoryMetadataLogged(ctx, installationID, owner, repo)
	return
}

// ResolveDefaultBranchCommit logs and executes the GitHub semantic operation.
func (c *Client) ResolveDefaultBranchCommit(ctx context.Context, installationID int64, owner string, repo string, branch string) (result0 string, err error) {
	op := beginSemantic(ctx, "client.ResolveDefaultBranchCommit", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.resolveDefaultBranchCommitLogged(ctx, installationID, owner, repo, branch)
	return
}

// GetRepoTree logs and executes the GitHub semantic operation.
func (c *Client) GetRepoTree(ctx context.Context, installationID int64, owner string, repo string, commitSHA string) (result0 RepoTree, err error) {
	op := beginSemantic(ctx, "client.GetRepoTree", installationID, owner, repo, 0)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getRepoTreeLogged(ctx, installationID, owner, repo, commitSHA)
	return
}

// GetBlobContent logs and executes the immutable blob fetch operation.
func (c *Client) GetBlobContent(ctx context.Context, installationID int64, owner, repo string, file RepoTreeFile) (result string, err error) {
	op := beginSemantic(ctx, "client.GetBlobContent", installationID, owner, repo, 0,
		"file_path", file.Path, "blob_sha", file.SHA, "declared_bytes", file.Size)
	defer func() { op.finish(err, "result_bytes", len(result)) }()
	result, err = c.getBlobContentLogged(ctx, installationID, owner, repo, file)
	return
}

// GetIssue logs and executes the GitHub semantic operation.
func (c *Client) GetIssue(ctx context.Context, installationID int64, owner string, repo string, number int) (result0 *Issue, err error) {
	op := beginSemantic(ctx, "client.GetIssue", installationID, owner, repo, number)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getIssueLogged(ctx, installationID, owner, repo, number)
	return
}

// GetClosingIssues logs and executes the GitHub semantic operation.
func (c *Client) GetClosingIssues(ctx context.Context, installationID int64, owner string, repo string, prNumber int) (result0 []ClosingIssueRef, err error) {
	op := beginSemantic(ctx, "client.GetClosingIssues", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs(result0)...) }()
	result0, err = c.getClosingIssuesLogged(ctx, installationID, owner, repo, prNumber)
	return
}

// UpdateStickySection logs and executes the GitHub semantic operation.
func (c *Client) UpdateStickySection(ctx context.Context, installationID int64, owner string, repo string, prNumber int, stickyReviewID int64, section string, sectionMD string) (err error) {
	op := beginSemantic(ctx, "client.UpdateStickySection", installationID, owner, repo, prNumber)
	defer func() { op.finish(err, semanticResultAttrs()...) }()
	err = c.updateStickySectionLogged(ctx, installationID, owner, repo, prNumber, stickyReviewID, section, sectionMD)
	return
}
