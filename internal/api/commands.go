package api

import (
	"context"
	"fmt"
	"strings"

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
			resp, err = s.indexer.IndexOwnerPattern(ctx, owner, content, metadata)
		} else {
			resp, err = s.indexer.IndexRepoPattern(ctx, owner, repo, content, metadata)
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

// isCommentAddressedInDiff checks if the file referenced by the comment
// has changes in the current diff (heuristic: if the file appears in the diff, consider it addressed).
func isCommentAddressedInDiff(bc botComment, rawDiff string) bool {
	if bc.Path == "" {
		return false
	}
	return strings.Contains(rawDiff, "diff --git a/"+bc.Path+" b/"+bc.Path) ||
		strings.Contains(rawDiff, "+++ b/"+bc.Path)
}
