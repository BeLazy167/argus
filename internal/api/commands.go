package api

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gh "github.com/google/go-github/v68/github"

	ghpkg "github.com/BeLazy167/argus/internal/github"
	"github.com/BeLazy167/argus/internal/memory"
)

// handleRememberCommand parses @argus-eye remember, stores the pattern in DB
// (and optionally Supermemory), and posts confirmation.
func (s *Server) handleRememberCommand(ctx context.Context, evt ghpkg.IssueCommentEvent, owner, repo string, ghClient *ghpkg.Client, args string) {
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "eyes")

	// Parse --org flag as discrete token to avoid matching substrings like --org-prefix
	var isOrg bool
	var contentParts []string
	for _, f := range strings.Fields(args) {
		if f == "--org" {
			isOrg = true
		} else {
			contentParts = append(contentParts, f)
		}
	}
	content := strings.TrimSpace(strings.Join(contentParts, " "))
	if content == "" {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"Usage: `@argus-eye remember <pattern>` or `@argus-eye remember --org <pattern>`")
		return
	}

	// Look up installation
	inst, err := s.store.GetInstallationByGitHubID(ctx, evt.InstallationID)
	if err != nil {
		s.logger.Error("remember: lookup installation", "error", err)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}

	// Index in Supermemory
	var smID *string
	var smWarning string
	if s.indexer != nil {
		metadata := map[string]string{
			"source":     "remember_command",
			"created_by": evt.CommentAuthor,
		}
		var resp *memory.AddResponse
		if isOrg {
			resp, err = s.indexer.IndexOwnerPattern(ctx, owner, content, "", metadata)
		} else {
			resp, err = s.indexer.IndexRepoPattern(ctx, owner, repo, content, "", metadata)
		}
		if err != nil {
			s.logger.Error("remember: index in supermemory", "error", err)
			smWarning = "\n\n_Warning: semantic search indexing failed. Pattern saved to DB only._"
		} else if resp != nil {
			smID = &resp.ID
		}
	}

	// Look up repo for DB write
	var repoID *int64
	if !isOrg {
		dbRepo, err := s.store.GetRepoByFullName(ctx, evt.RepoFullName)
		if err != nil {
			s.logger.Error("remember: lookup repo for scoping", "error", err, "repo", evt.RepoFullName)
			_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
			_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
				"Failed to scope pattern to this repo. Please try again.")
			return
		}
		repoID = &dbRepo.ID
	}

	createdBy := evt.CommentAuthor
	_, err = s.store.CreatePattern(ctx, inst.ID, repoID, content, smID, &createdBy)
	if err != nil {
		s.logger.Error("remember: save to db", "error", err)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}

	scope := "this repo"
	if isOrg {
		scope = "org-wide"
	}
	truncated := content
	if len(truncated) > 100 {
		truncated = truncated[:100] + "..."
	}
	_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
		fmt.Sprintf("Remembered (%s): %s%s", scope, truncated, smWarning))
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
}

// handleResolveCommand lists all bot review comments on a PR, checks if their
// referenced files appear in the latest diff, and minimizes those that appear addressed.
func (s *Server) handleResolveCommand(ctx context.Context, evt ghpkg.IssueCommentEvent, owner, repo string, ghClient *ghpkg.Client) {
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "eyes")

	// List all review comments on the PR
	allComments, err := ghClient.ListPRComments(ctx, evt.InstallationID, owner, repo, evt.PRNumber)
	if err != nil {
		s.logger.Error("resolve: list comments", "error", err)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}

	// Filter to bot comments
	var botComments []botComment
	for _, c := range allComments {
		if c.GetUser() != nil && strings.HasSuffix(c.GetUser().GetLogin(), "[bot]") {
			botComments = append(botComments, botComment{
				NodeID: c.GetNodeID(),
				Body:   c.GetBody(),
				Path:   c.GetPath(),
				Line:   c.GetLine(),
			})
		}
	}

	if len(botComments) == 0 {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"No open review comments to resolve.")
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
		return
	}

	// Fetch latest diff
	rawDiff, err := ghClient.GetPRDiff(ctx, evt.InstallationID, owner, repo, evt.PRNumber)
	if err != nil {
		s.logger.Error("resolve: fetch diff", "error", err)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}

	// Check each comment against the diff
	var resolved, stillOpen int
	for _, bc := range botComments {
		if isCommentAddressedInDiff(bc, rawDiff) {
			if err := ghClient.MinimizeComment(ctx, evt.InstallationID, bc.NodeID, "RESOLVED"); err != nil {
				s.logger.Error("resolve: minimize comment", "error", err, "node_id", bc.NodeID)
				stillOpen++
			} else {
				resolved++
			}
		} else {
			stillOpen++
		}
	}

	_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
		fmt.Sprintf("Resolve complete: **%d addressed**, **%d still open**.", resolved, stillOpen))
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
}

type botComment struct {
	NodeID string
	Body   string
	Path   string
	Line   int
}

// fileFix represents a single suggestion block extracted from a bot review comment.
type fileFix struct {
	path       string
	line       int
	startLine  int
	suggestion string
}

// isCommentAddressedInDiff checks if the file referenced by the comment
// has changes in the current diff (heuristic: if the file appears in the diff, consider it addressed).
func isCommentAddressedInDiff(bc botComment, rawDiff string) bool {
	if bc.Path == "" {
		return false
	}
	return strings.Contains(rawDiff, "diff --git a/"+bc.Path+" b/"+bc.Path) ||
		strings.Contains(rawDiff, "+++ b/"+bc.Path)
}

// handleFixCommand applies suggestion blocks from bot review comments as a commit.
func (s *Server) handleFixCommand(ctx context.Context, evt ghpkg.IssueCommentEvent, owner, repo string, ghClient *ghpkg.Client) {
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "eyes")

	// Get PR details for head ref
	pr, err := ghClient.GetPullRequest(ctx, evt.InstallationID, owner, repo, evt.PRNumber)
	if err != nil {
		s.logger.Error("fix: getting PR", "error", err)
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, "Failed to get PR details: "+err.Error())
		return
	}

	fixes, err := collectBotFixes(ctx, evt, owner, repo, ghClient)
	if err != nil {
		s.logger.Error("fix: listing comments", "error", err)
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, "Failed to list review comments: "+err.Error())
		return
	}
	if len(fixes) == 0 {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"No suggestion blocks found in review comments. Nothing to fix.")
		return
	}

	// Group fixes by file
	fileFixMap := make(map[string][]fileFix)
	for _, f := range fixes {
		fileFixMap[f.path] = append(fileFixMap[f.path], f)
	}

	// Get current head SHA and tree
	headSHA, err := ghClient.GetRef(ctx, evt.InstallationID, owner, repo, "heads/"+pr.HeadRef)
	if err != nil {
		s.logger.Error("fix: getting ref", "error", err)
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, "Failed to get branch head: "+err.Error())
		return
	}
	baseTreeSHA, err := ghClient.GetCommitTree(ctx, evt.InstallationID, owner, repo, headSHA)
	if err != nil {
		s.logger.Error("fix: getting tree", "error", err)
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, "Failed to read commit tree: "+err.Error())
		return
	}

	// Apply fixes per file, create blobs
	var treeEntries []*gh.TreeEntry
	appliedCount := 0
	for path, pathFixes := range fileFixMap {
		content, err := ghClient.GetFileContent(ctx, evt.InstallationID, owner, repo, path, headSHA)
		if err != nil {
			s.logger.Warn("fix: fetching file", "path", path, "error", err)
			continue
		}

		// Apply fixes in reverse line order to avoid offset shifts
		lines := strings.Split(content, "\n")
		sort.Slice(pathFixes, func(i, j int) bool {
			return pathFixes[i].startLine > pathFixes[j].startLine
		})
		fileApplied := 0
		lowestModified := len(lines) + 1
		for _, fix := range pathFixes {
			if fix.startLine < 1 || fix.line > len(lines) {
				s.logger.Warn("fix: skipping out-of-range suggestion", "path", path, "startLine", fix.startLine, "line", fix.line, "fileLines", len(lines))
				continue
			}
			if fix.line >= lowestModified {
				s.logger.Warn("fix: skipping overlapping suggestion", "path", path, "line", fix.line, "lowestModified", lowestModified)
				continue
			}
			suggestionLines := strings.Split(fix.suggestion, "\n")
			newLines := make([]string, 0, len(lines)-fix.line+fix.startLine-1+len(suggestionLines))
			newLines = append(newLines, lines[:fix.startLine-1]...)
			newLines = append(newLines, suggestionLines...)
			newLines = append(newLines, lines[fix.line:]...)
			lines = newLines
			fileApplied++
			lowestModified = fix.startLine
		}

		newContent := strings.Join(lines, "\n")
		blobSHA, err := ghClient.CreateBlob(ctx, evt.InstallationID, owner, repo, newContent, "utf-8")
		if err != nil {
			s.logger.Error("fix: creating blob", "path", path, "error", err)
			continue
		}
		appliedCount += fileApplied
		treeEntries = append(treeEntries, &gh.TreeEntry{
			Path: gh.Ptr(path),
			Mode: gh.Ptr("100644"),
			Type: gh.Ptr("blob"),
			SHA:  gh.Ptr(blobSHA),
		})
	}

	if len(treeEntries) == 0 {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"Could not apply any suggestions. The code may have changed since the review.")
		return
	}

	// Atomic commit
	treeSHA, err := ghClient.CreateTree(ctx, evt.InstallationID, owner, repo, baseTreeSHA, treeEntries)
	if err != nil {
		s.logger.Error("fix: creating tree", "error", err)
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, "Failed to create fix commit tree: "+err.Error())
		return
	}

	commitMsg := fmt.Sprintf("fix: apply %d Argus suggestions", appliedCount)
	commitSHA, err := ghClient.CreateCommit(ctx, evt.InstallationID, owner, repo, commitMsg, treeSHA, []string{headSHA})
	if err != nil {
		s.logger.Error("fix: creating commit", "error", err)
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, "Failed to create fix commit: "+err.Error())
		return
	}

	if err := ghClient.UpdateRef(ctx, evt.InstallationID, owner, repo, "heads/"+pr.HeadRef, commitSHA); err != nil {
		s.logger.Error("fix: updating ref", "error", err)
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"Failed to push fix commit. The bot may not have write access to fork branches.")
		return
	}

	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
	_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
		fmt.Sprintf("Applied **%d suggestions** across **%d files** in commit `%.7s`.", appliedCount, len(treeEntries), commitSHA))
}

// collectBotFixes lists PR review comments and extracts suggestion blocks from bot comments.
func collectBotFixes(ctx context.Context, evt ghpkg.IssueCommentEvent, owner, repo string, ghClient *ghpkg.Client) ([]fileFix, error) {
	comments, err := ghClient.ListPRComments(ctx, evt.InstallationID, owner, repo, evt.PRNumber)
	if err != nil {
		return nil, err
	}

	var fixes []fileFix
	for _, c := range comments {
		if c.GetUser() == nil || !strings.HasSuffix(c.GetUser().GetLogin(), "[bot]") {
			continue
		}
		suggestion := parseSuggestionBlock(c.GetBody())
		if suggestion == "" {
			continue
		}
		line := c.GetLine()
		startLine := c.GetStartLine()
		if startLine == 0 {
			startLine = line
		}
		fixes = append(fixes, fileFix{
			path:       c.GetPath(),
			line:       line,
			startLine:  startLine,
			suggestion: suggestion,
		})
	}
	return fixes, nil
}

// parseSuggestionBlock extracts content between ```suggestion and ``` from a comment body.
func parseSuggestionBlock(body string) string {
	const marker = "```suggestion"
	start := strings.Index(body, marker)
	if start == -1 {
		return ""
	}
	start += len(marker)
	if start < len(body) && body[start] == '\n' {
		start++
	}
	end := strings.Index(body[start:], "\n```")
	if end == -1 {
		return ""
	}
	suggestion := body[start : start+end]
	return strings.TrimRight(suggestion, "\n")
}
