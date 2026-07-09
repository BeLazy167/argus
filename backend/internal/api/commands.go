package api

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	gh "github.com/google/go-github/v68/github"

	ghpkg "github.com/BeLazy167/argus/backend/internal/github"
	"github.com/BeLazy167/argus/backend/internal/llm"
	"github.com/BeLazy167/argus/backend/internal/memory"
	"github.com/BeLazy167/argus/backend/internal/pipeline"
	"github.com/BeLazy167/argus/backend/internal/store"
)

// --- Command Dispatch ---

var commandRe = regexp.MustCompile(`(?i)@argus-eye\s+(review|remember|resolve|fix|test|help)(.*)`)

func (s *Server) dispatchCommand(ctx context.Context, evt ghpkg.IssueCommentEvent) {
	match := commandRe.FindStringSubmatch(strings.TrimSpace(evt.CommentBody))
	if match == nil {
		return
	}

	parts := strings.SplitN(evt.RepoFullName, "/", 2)
	if len(parts) != 2 {
		return
	}
	owner, repo := parts[0], parts[1]
	ghClient := ghpkg.NewClient(s.ghApp)

	cmd := strings.ToLower(match[1])
	args := strings.TrimSpace(match[2])

	switch cmd {
	case "review":
		s.handleReviewCommand(ctx, evt, owner, repo, ghClient, args)
	case "remember":
		s.handleRememberCommand(ctx, evt, owner, repo, ghClient, args)
	case "resolve":
		s.handleResolveCommand(ctx, evt, owner, repo, ghClient)
	case "fix":
		s.handleFixCommand(ctx, evt, owner, repo, ghClient)
	case "test":
		s.handleTestCommand(ctx, evt, owner, repo, ghClient, args)
	case "help":
		s.handleHelpCommand(ctx, evt, owner, repo, ghClient)
	}
}

func (s *Server) handleReviewCommand(ctx context.Context, evt ghpkg.IssueCommentEvent, owner, repo string, ghClient *ghpkg.Client, args string) {
	force := strings.Contains(args, "--force")
	var personaOverride string
	if idx := strings.Index(args, "--persona"); idx >= 0 {
		rest := strings.TrimSpace(args[idx+len("--persona"):])
		if fields := strings.Fields(rest); len(fields) > 0 {
			personaOverride = fields[0]
		}
	}

	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "eyes")

	prEvent, err := ghClient.GetPullRequest(ctx, evt.InstallationID, owner, repo, evt.PRNumber)
	if err != nil {
		s.logger.Error("review command: fetch PR failed", "error", err, "pr", evt.PRNumber)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}

	if !force {
		existing, err := s.store.GetLatestReviewBySHA(ctx, evt.RepoFullName, evt.PRNumber, prEvent.HeadSHA)
		if err == nil && existing != nil {
			short := prEvent.HeadSHA
			if len(short) > 7 {
				short = short[:7]
			}
			body := fmt.Sprintf("Already reviewed at `%s`. Use `@argus-eye review --force` to re-review.", short)
			_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, body)
			_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
			return
		}
	}

	if !s.allowReview(ctx, evt.RepoFullName, owner, force, evt.InstallationID) {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"Rate limit exceeded. Try again later.")
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}
	if !s.tryAcquireReview(evt.RepoFullName, evt.PRNumber) {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"A review is already in progress for this PR.")
		return
	}
	defer s.releaseReview(evt.RepoFullName, evt.PRNumber)

	// Register a cancel function so the dashboard's Stop button can abort this
	// review. The webhook and manual-API paths already do this; the slash-
	// command path was missing it — clicking Stop returned HTTP 409 ("review
	// not in-flight") because loadCancel found nothing in inFlightCancels.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.storeCancel(evt.RepoFullName, evt.PRNumber, cancel)
	defer s.removeCancel(evt.RepoFullName, evt.PRNumber)

	prEvent.Action = "manual"
	prEvent.RepoID = evt.RepoID
	prEvent.PersonaOverride = personaOverride
	s.logger.Info("review command triggered", "repo", evt.RepoFullName, "pr", evt.PRNumber, "force", force, "by", evt.CommentAuthor)

	if err := s.orchestrator.HandlePREvent(runCtx, *prEvent); err != nil {
		s.logger.Error("review command: pipeline failed", "error", err, "pr", evt.PRNumber)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"Review failed. Check the Argus dashboard for details.")
		return
	}

	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
}

// handleHelpCommand posts available commands and usage.
func (s *Server) handleHelpCommand(ctx context.Context, evt ghpkg.IssueCommentEvent, owner, repo string, ghClient *ghpkg.Client) {
	help := `### Argus Commands

| Command | Description |
|---------|-------------|
| ` + "`@argus-eye review`" + ` | Trigger a code review on this PR |
| ` + "`@argus-eye review --force`" + ` | Re-review even if already reviewed at this SHA |
| ` + "`@argus-eye review --persona <name>`" + ` | Review with a specific persona |
| | _Personas: default, security_auditor, performance_engineer, mentor, architect, strict, adversarial, fresh_eyes_ |
| ` + "`@argus-eye remember <pattern>`" + ` | Teach Argus a pattern for this repo |
| ` + "`@argus-eye remember --org <pattern>`" + ` | Teach Argus an org-wide pattern |
| ` + "`@argus-eye fix`" + ` | Apply all suggestion blocks from review comments as a commit |
| ` + "`@argus-eye resolve`" + ` | Resolve all open Argus review threads on this PR |
| ` + "`@argus-eye test`" + ` | Generate a test plan for review findings |
| ` + "`@argus-eye test --code`" + ` | Draft test code for review findings |
| ` + "`@argus-eye help`" + ` | Show this message |`

	_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, help)
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
}

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
	if s.memRegistry != nil {
		indexer := s.memRegistry.GetIndexer(ctx, inst.ID)
		if indexer != nil {
			metadata := map[string]string{
				"source":     "remember_command",
				"created_by": evt.CommentAuthor,
			}
			var resp *memory.AddResponse
			if isOrg {
				resp, err = indexer.IndexOwnerPattern(ctx, owner, content, "", metadata)
			} else {
				resp, err = indexer.IndexRepoPattern(ctx, owner, repo, content, "", metadata)
			}
			if err != nil {
				s.logger.Error("remember: index in supermemory", "error", err)
				smWarning = "\n\n_Warning: semantic search indexing failed. Pattern saved to DB only._"
			} else if resp != nil {
				smID = &resp.ID
			}
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
	_, err = s.store.CreatePattern(ctx, inst.ID, repoID, content, smID, &createdBy, nil, nil, nil)
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

// isArgusCommentAuthor reports whether an issue_comment was authored by Argus.
// Used by the checkbox-trigger path to reject hijack attempts where a
// collaborator pastes the trigger-marker + checkbox into their own comment and
// toggles it: without this guard, any repo member could spin up reviews by
// impersonating our comment format.
//
// Canonical identity lives in ghpkg.IsArgusThread; this alias preserves the
// naming intent at the call site (we're checking comment authorship, not a
// thread) while keeping the rule in one place.
func isArgusCommentAuthor(authorLogin string) bool {
	return ghpkg.IsArgusThread(authorLogin)
}

// classifyResolveError summarizes an error from ResolveReviewThread into a
// short, user-facing phrase. Returns "" for errors we don't recognize (the
// caller will fall back to a generic "GraphQL error" message). The `fatal`
// return is true when the error is systemic (auth, permissions) — the caller
// should abort the loop rather than retry N times.
//
// The `not accessible` case covers GitHub's GraphQL-level scope error
// "Resource not accessible by integration", which is what the App returns
// when it lacks `pull_requests: write` on the installation. This is the same
// class of failure as 403, but doesn't carry an HTTP status string because
// GraphQL mutations return 200 + a structured error payload.
func classifyResolveError(err error) (phrase string, fatal bool) {
	if err == nil {
		return "", false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "401") || strings.Contains(msg, "unauthorized"):
		return "authentication failed — Argus may need to be reinstalled on this repo", true
	case strings.Contains(msg, "not accessible by integration"),
		strings.Contains(msg, "403"),
		strings.Contains(msg, "forbidden"),
		strings.Contains(msg, "permission"):
		return "missing permission — grant the Argus GitHub App `Pull requests: write` access (https://github.com/settings/installations)", true
	case strings.Contains(msg, "404") || strings.Contains(msg, "not found"):
		return "thread already resolved or deleted upstream", false
	case strings.Contains(msg, "rate limit") || strings.Contains(msg, "secondary rate limit"):
		return "GitHub API rate limit hit — retry in a few minutes", true
	}
	return "", false
}

// handleResolveCommand resolves every open Argus review thread on the PR.
// The command is an explicit user signal ("I've handled these, close them"),
// so we trust the caller rather than second-guess with diff heuristics.
// Auto-resolve on push (orchestrator.autoResolveOnSynchronize)
// stays the cautious path; manual invocation is the trust-the-operator path.
func (s *Server) handleResolveCommand(ctx context.Context, evt ghpkg.IssueCommentEvent, owner, repo string, ghClient *ghpkg.Client) {
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "eyes")

	threads, err := ghClient.ListReviewThreads(ctx, evt.InstallationID, owner, repo, evt.PRNumber)
	if err != nil {
		s.logger.Error("resolve: list threads", "error", err)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}

	var unresolvedBot []ghpkg.ReviewThread
	for _, t := range threads {
		if !t.IsResolved && ghpkg.IsArgusThread(t.AuthorLogin) {
			unresolvedBot = append(unresolvedBot, t)
		}
	}

	if len(unresolvedBot) == 0 {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"No open Argus review threads to resolve.")
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
		return
	}

	var resolved, failed int
	var firstErrPhrase string // classified reason to surface to the user
	for _, t := range unresolvedBot {
		resolveErr := ghClient.ResolveReviewThread(ctx, evt.InstallationID, t.ID)
		if resolveErr == nil {
			resolved++
			continue
		}
		s.logger.Error("resolve: resolve thread", "error", resolveErr, "thread_id", t.ID)
		failed++
		phrase, fatal := classifyResolveError(resolveErr)
		if firstErrPhrase == "" && phrase != "" {
			firstErrPhrase = phrase
		}
		if fatal {
			// Systemic failure: abort early rather than loop N times hitting the same wall.
			break
		}
	}

	suffix := "s"
	if resolved == 1 {
		suffix = ""
	}
	msg := fmt.Sprintf("Resolved **%d** Argus review thread%s.", resolved, suffix)
	if failed > 0 {
		if firstErrPhrase != "" {
			msg += fmt.Sprintf(" %d failed: %s.", failed, firstErrPhrase)
		} else {
			msg += fmt.Sprintf(" %d failed to resolve (check dashboard logs for details).", failed)
		}
	}
	_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, msg)
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
}

// fileFix represents a single suggestion block extracted from a bot review comment.
type fileFix struct {
	path       string
	line       int
	startLine  int
	suggestion string
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
			"Failed to push fix commit. Argus needs write access to create commits. Check your GitHub App permissions at https://github.com/settings/installations")
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

// handleTestCommand generates a test plan or draft test code from review findings.
func (s *Server) handleTestCommand(ctx context.Context, evt ghpkg.IssueCommentEvent, owner, repo string, ghClient *ghpkg.Client, args string) {
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "eyes")

	review, err := s.store.GetLatestReviewByPR(ctx, fmt.Sprintf("%s/%s", owner, repo), evt.PRNumber)
	if err != nil || review == nil {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"No review found for this PR. Run `@argus-eye review` first.")
		return
	}

	comments, err := s.store.GetReviewComments(ctx, review.ID)
	if err != nil || len(comments) == 0 {
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			"No findings to generate tests for.")
		return
	}

	rawDiff, err := ghClient.GetPRDiff(ctx, evt.InstallationID, owner, repo, evt.PRNumber)
	if err != nil {
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}

	wantCode := strings.Contains(args, "--code")

	inst, err := s.store.GetInstallationByGitHubID(ctx, evt.InstallationID)
	if err != nil {
		return
	}

	lister := pipeline.StoreConfigListerFor(s.store, inst.ID)
	provider, cfg, err := s.registry.ResolveProvider(ctx, lister, inst.ID, 0, llm.StageReview)
	if err != nil {
		// Deep-link the Settings URL to this specific repo so the user lands on
		// the right row instead of whatever was last selected in localStorage.
		settingsURL := "https://argus.reviews/settings"
		if dbRepo, rerr := s.store.GetRepoByFullName(ctx, evt.RepoFullName); rerr == nil && dbRepo != nil {
			settingsURL = fmt.Sprintf("https://argus.reviews/settings?repo=%d", dbRepo.ID)
		}
		_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber,
			fmt.Sprintf("No LLM provider configured. Add an API key at your [Argus Settings](%s).", settingsURL))
		return
	}

	var prompt string
	if wantCode {
		prompt = buildTestCodePrompt(comments, rawDiff)
	} else {
		prompt = buildTestPlanPrompt(comments, rawDiff)
	}

	resp, err := provider.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.Model,
		System:      testGenerationSystemPrompt,
		Messages:    []llm.Message{{Role: "user", Content: prompt}},
		MaxTokens:   2000,
		Temperature: 0.3,
		Stage:       "test_gen",
	})
	if err != nil {
		s.logger.Error("test generation failed", "error", err)
		_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "confused")
		return
	}

	header := "## Test Plan"
	if wantCode {
		header = "## Draft Test Code"
	}
	body := header + "\n\n" + strings.TrimSpace(resp.Content)

	_ = ghClient.CreateIssueComment(ctx, evt.InstallationID, owner, repo, evt.PRNumber, body)
	_ = ghClient.AddReaction(ctx, evt.InstallationID, owner, repo, evt.CommentID, "rocket")
}

const testGenerationSystemPrompt = `You are a senior test engineer. Given code review findings and a PR diff, generate comprehensive test suggestions. Be specific — reference exact functions, edge cases, and expected behaviors. Use the project's existing test patterns if visible in the diff.`

func buildTestPlanPrompt(comments []store.ReviewComment, rawDiff string) string {
	var sb strings.Builder
	sb.WriteString("## Review Findings\n\n")
	for _, c := range comments {
		sb.WriteString(fmt.Sprintf("- [%s] %s:%d — %s\n",
			stringOr(c.Severity, "info"), c.FilePath, intOr(c.EndLine, 0), c.Body))
	}
	sb.WriteString("\n## PR Diff\n```\n")
	if len(rawDiff) > 5000 {
		rawDiff = rawDiff[:5000] + "\n...(truncated)"
	}
	sb.WriteString(rawDiff)
	sb.WriteString("\n```\n\n")
	sb.WriteString("Generate a test plan covering:\n1. Unit tests for each finding\n2. Edge cases identified in the review\n3. Integration tests for cross-file impacts\n4. Regression tests for previously broken behaviors\n\nFormat as a markdown checklist.")
	return sb.String()
}

func buildTestCodePrompt(comments []store.ReviewComment, rawDiff string) string {
	var sb strings.Builder
	sb.WriteString("## Review Findings\n\n")
	for _, c := range comments {
		sb.WriteString(fmt.Sprintf("- [%s] %s:%d — %s\n",
			stringOr(c.Severity, "info"), c.FilePath, intOr(c.EndLine, 0), c.Body))
	}
	sb.WriteString("\n## PR Diff\n```\n")
	if len(rawDiff) > 5000 {
		rawDiff = rawDiff[:5000] + "\n...(truncated)"
	}
	sb.WriteString(rawDiff)
	sb.WriteString("\n```\n\n")
	sb.WriteString("Generate executable test code for the most critical findings. Match the project's testing framework (detect from imports in the diff). Include:\n- Setup/teardown\n- Edge case coverage\n- Assertion messages\n\nOutput as a single fenced code block per test file.")
	return sb.String()
}

func stringOr(s *string, def string) string {
	if s != nil {
		return *s
	}
	return def
}

func intOr(i *int, def int) int {
	if i != nil {
		return *i
	}
	return def
}
