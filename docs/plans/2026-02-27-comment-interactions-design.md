# Argus Comment Interaction System — Design Doc

**Date:** 2026-02-27
**Status:** Draft

---

## 1. Overview

Add the ability for developers to interact with the Argus bot via PR comments. When a user @mentions `@argus-bot` (the GitHub App name) in a PR comment, Argus parses the command and dispatches to the appropriate handler.

### Supported Commands

| Command | Trigger | Action |
|---------|---------|--------|
| Re-review | `@argus re-review` / `@argus look again` | Trigger full or file-scoped re-review |
| Resolve | `@argus resolve` | Resolve/dismiss all Argus review threads |
| Create Issue | `@argus create issue <description>` | Create a GitHub Issue from comment context |
| Describe | `@argus describe` | Generate and update PR description from diff |
| Summarize | `@argus summarize` | Post a high-level summary comment on the PR |
| Diagram | `@argus diagram` | Generate a Mermaid sequence diagram of the code flow |

---

## 2. Architecture

### 2.1 Webhook Event Flow

```
GitHub Webhook
  ├── issue_comment (created)          ← comments on PR (not on a specific line)
  └── pull_request_review_comment (created) ← replies to review line comments
        │
        ▼
  ParseWebhook() → WebhookEvent
        │
        ▼
  handleWebhook() switch on event.Type
        │
        ▼
  ToCommentEvent() → CommentEvent
        │
        ▼
  orchestrator.HandleCommentEvent()
        │
        ▼
  CommandParser.Parse(body) → Command
        │
        ▼
  CommandDispatcher.Dispatch(cmd, event)
        │
        ├── re-review   → orchestrator.HandlePREvent() (reuse existing)
        ├── resolve      → ghClient.ResolveReviewThreads()
        ├── create-issue → ghClient.CreateIssue()
        ├── describe     → LLM generate → ghClient.UpdatePRBody()
        ├── summarize    → LLM generate → ghClient.PostComment()
        └── diagram      → LLM generate → ghClient.PostComment()
```

### 2.2 Key Design Decisions

1. **Two webhook event types.** GitHub sends `issue_comment` for top-level PR comments and `pull_request_review_comment` for replies to review threads. We need both — users might @mention Argus in either context.

2. **Unified CommentEvent struct.** Both event types are normalized into a single `CommentEvent` so downstream handlers don't care about the source.

3. **Command parsing is regex-based, not LLM-based.** Fast, deterministic, no token cost. LLM is only used for generation commands (describe, summarize, diagram).

4. **Reaction-based acknowledgment.** On receiving a valid command, Argus reacts with `eyes` emoji on the comment immediately, then replaces with `rocket` on success or `confused` on failure. This gives fast feedback without posting noisy comments.

5. **Reuse existing pipeline for re-review.** The `HandlePREvent` path already handles full and incremental reviews. For re-review, we construct a synthetic PREvent with `action: "comment_triggered"`.

---

## 3. New Types

### 3.1 `internal/github/webhooks.go` — CommentEvent

```go
// CommentEvent holds parsed data from issue_comment or pull_request_review_comment webhooks.
type CommentEvent struct {
    Action         string // "created", "edited", "deleted"
    InstallationID int64
    RepoFullName   string
    RepoID         int64
    PRNumber       int
    CommentID      int64
    CommentBody    string
    CommentAuthor  string
    // Only set for pull_request_review_comment events
    InReplyToID    *int64 // The review comment being replied to
    ReviewID       *int64 // The GitHub review ID
    FilePath       *string
    DiffHunk       *string
}
```

### 3.2 `internal/pipeline/commands.go` — Command Types

```go
type CommandKind string

const (
    CmdReReview    CommandKind = "re-review"
    CmdResolve     CommandKind = "resolve"
    CmdCreateIssue CommandKind = "create-issue"
    CmdDescribe    CommandKind = "describe"
    CmdSummarize   CommandKind = "summarize"
    CmdDiagram     CommandKind = "diagram"
)

type Command struct {
    Kind    CommandKind
    Args    string   // free-text after the command keyword
    FilePath *string // if scoped to a specific file (from review comment context)
}
```

---

## 4. Command Parsing

### 4.1 Approach

Regex-based parser that:
1. Checks if comment body contains `@argus` (case-insensitive, configurable bot name)
2. Extracts the command keyword after the mention
3. Captures remaining text as args

```go
// Pattern: @argus <command> [args...]
// Bot name is configurable (could be @argus-bot, @argus-review, etc.)
var mentionPattern = regexp.MustCompile(`(?i)@argus[-\w]*\s+(re-?review|look again|resolve|create\s+issue|describe|summarize|summary|diagram)(.*)`)
```

### 4.2 Command Aliases

| Canonical | Aliases |
|-----------|---------|
| re-review | `re-review`, `rereview`, `look again`, `review again` |
| resolve | `resolve`, `dismiss` |
| create-issue | `create issue`, `open issue`, `file issue` |
| describe | `describe`, `description` |
| summarize | `summarize`, `summary` |
| diagram | `diagram`, `sequence diagram` |

### 4.3 Bot Name Discovery

The GitHub App's "slug" (login name) is available via the `GET /app` endpoint. On startup, Argus should cache this and use it for mention matching. Fallback: configurable via env var `ARGUS_BOT_NAME`.

---

## 5. Webhook Handling Changes

### 5.1 `internal/github/webhooks.go`

Add `ToCommentEvent` converter functions:

```go
func ToIssueCommentEvent(event *WebhookEvent) (*CommentEvent, error) {
    e, ok := event.Payload.(*gh.IssueCommentEvent)
    if !ok {
        return nil, fmt.Errorf("expected IssueCommentEvent, got %T", event.Payload)
    }
    // Only process comments on PRs (issue_comment fires for both issues and PRs)
    if e.GetIssue().PullRequestLinks == nil {
        return nil, nil // not a PR comment, ignore
    }
    return &CommentEvent{
        Action:         event.Action,
        InstallationID: e.GetInstallation().GetID(),
        RepoFullName:   e.GetRepo().GetFullName(),
        RepoID:         e.GetRepo().GetID(),
        PRNumber:       e.GetIssue().GetNumber(),
        CommentID:      e.GetComment().GetID(),
        CommentBody:    e.GetComment().GetBody(),
        CommentAuthor:  e.GetComment().GetUser().GetLogin(),
    }, nil
}

func ToPRReviewCommentEvent(event *WebhookEvent) (*CommentEvent, error) {
    e, ok := event.Payload.(*gh.PullRequestReviewCommentEvent)
    if !ok {
        return nil, fmt.Errorf("expected PullRequestReviewCommentEvent, got %T", event.Payload)
    }
    return &CommentEvent{
        Action:         event.Action,
        InstallationID: e.GetInstallation().GetID(),
        RepoFullName:   e.GetRepo().GetFullName(),
        RepoID:         e.GetRepo().GetID(),
        PRNumber:       e.GetPullRequest().GetNumber(),
        CommentID:      e.GetComment().GetID(),
        CommentBody:    e.GetComment().GetBody(),
        CommentAuthor:  e.GetComment().GetUser().GetLogin(),
        InReplyToID:    int64Ptr(e.GetComment().GetInReplyTo()),
        ReviewID:       int64Ptr(e.GetPullRequest().GetID()), // review context
        FilePath:       strPtr(e.GetComment().GetPath()),
        DiffHunk:       strPtr(e.GetComment().GetDiffHunk()),
    }, nil
}
```

Update `extractAction` to handle `*gh.IssueCommentEvent`.

### 5.2 `internal/api/server.go` — handleWebhook

Add cases for the two new event types:

```go
case "issue_comment":
    commentEvent, err := ghpkg.ToIssueCommentEvent(event)
    if err != nil {
        // ...
    }
    if commentEvent == nil {
        break // not a PR comment
    }
    go func() {
        if err := s.orchestrator.HandleCommentEvent(context.Background(), *commentEvent); err != nil {
            s.logger.Error("comment handler failed", "error", err, "pr", commentEvent.PRNumber)
        }
    }()

case "pull_request_review_comment":
    commentEvent, err := ghpkg.ToPRReviewCommentEvent(event)
    if err != nil {
        // ...
    }
    go func() {
        if err := s.orchestrator.HandleCommentEvent(context.Background(), *commentEvent); err != nil {
            s.logger.Error("comment handler failed", "error", err, "pr", commentEvent.PRNumber)
        }
    }()
```

---

## 6. New GitHub Client Methods

Add to `internal/github/client.go`:

```go
// PostComment posts a top-level comment on a PR (issue comment).
func (c *Client) PostComment(ctx context.Context, installationID int64, owner, repo string, prNumber int, body string) (int64, error)

// AddReaction adds an emoji reaction to a comment.
func (c *Client) AddReaction(ctx context.Context, installationID int64, owner, repo string, commentID int64, reaction string) error

// RemoveReaction removes an emoji reaction from a comment.
func (c *Client) RemoveReaction(ctx context.Context, installationID int64, owner, repo string, commentID int64, reactionID int64) error

// ResolveReviewThreads marks all Argus review threads on a PR as resolved.
// GitHub GraphQL API is required — REST API does not support resolving threads.
func (c *Client) ResolveReviewThreads(ctx context.Context, installationID int64, owner, repo string, prNumber int) error

// CreateIssue creates a new issue in the repo.
func (c *Client) CreateIssue(ctx context.Context, installationID int64, owner, repo, title, body string, labels []string) (int, error)

// UpdatePRBody updates the body/description of a pull request.
func (c *Client) UpdatePRBody(ctx context.Context, installationID int64, owner, repo string, prNumber int, body string) error

// GetPR fetches pull request metadata (title, body, author, labels, etc.).
func (c *Client) GetPR(ctx context.Context, installationID int64, owner, repo string, prNumber int) (*gh.PullRequest, error)

// ListPRReviewComments lists all review comments on a PR (for filtering Argus comments when resolving).
func (c *Client) ListPRReviewComments(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]*gh.PullRequestComment, error)
```

### GraphQL Requirement for Resolve

GitHub's REST API does not support resolving review threads. We need the GraphQL API:

```graphql
mutation {
  resolveReviewThread(input: { threadId: "<node_id>" }) {
    thread { isResolved }
  }
}
```

This requires:
1. List all review comments on the PR
2. Filter to Argus's own comments (by bot user ID)
3. Extract the `node_id` for each thread
4. Call `resolveReviewThread` mutation for each

We can use `go-github`'s `GraphQLClient` or make raw GraphQL HTTP calls. Since this is the only GraphQL usage, raw HTTP is simpler.

---

## 7. Command Handlers — Detailed Flows

### 7.1 Re-Review (`@argus re-review`)

**Flow:**
1. Parse command. If file-scoped (from a review comment on a specific file), set target file
2. Look up repo in DB via `RepoFullName`
3. Construct a synthetic `PREvent` with `Action: "comment_triggered"`
4. Call existing `orchestrator.HandlePREvent()`
5. The existing pipeline handles triage → review → synthesis → post

**File-scoped re-review (stretch goal):**
- If the command comes from a `pull_request_review_comment` with a `FilePath`, pass it through
- Modify `HandlePREvent` to accept an optional file filter, or handle in triage by forcing that file to `deep`

### 7.2 Resolve (`@argus resolve`)

**Flow:**
1. Get the bot's user ID (cache on startup via `GET /app`)
2. List all review comments on the PR
3. Filter to comments authored by the bot
4. For each unique thread (grouped by `node_id`), call GraphQL `resolveReviewThread`
5. Post confirmation comment: "Resolved N review threads."

### 7.3 Create Issue (`@argus create issue`)

**Flow:**
1. Parse the args after "create issue" as issue description
2. Build issue context from:
   - The comment body (user's description)
   - The diff hunk (if from a review comment)
   - The PR title and number for cross-reference
3. Use LLM to generate a structured issue title + body from the context
4. Call `ghClient.CreateIssue()`
5. Reply with a comment linking to the new issue: "Created issue #123"

**LLM Prompt:**
```
Given this PR context and user request, generate a GitHub issue.
PR: #42 "Add user authentication"
User says: "@argus create issue this error handling needs a proper retry mechanism"
File context: <diff hunk if available>

Generate JSON: {"title": "...", "body": "...", "labels": ["bug"|"enhancement"|...]}
```

### 7.4 Describe (`@argus describe`)

**Flow:**
1. Fetch the full PR diff via existing `ghClient.GetPRDiff()`
2. Fetch the current PR body via `ghClient.GetPR()`
3. Send diff to LLM with a prompt to generate a PR description
4. Call `ghClient.UpdatePRBody()` with the generated description
5. React with `rocket` on success

**LLM Prompt:**
```
Generate a pull request description for the following changes.
PR title: "Add user authentication"
Files changed: auth.go, middleware.go, auth_test.go

<diff>

Write a clear, structured PR description with:
- Summary (1-2 sentences)
- Changes (bullet points per file/feature)
- Testing notes (if test files are in the diff)

Use markdown. Be concise.
```

### 7.5 Summarize (`@argus summarize`)

**Flow:**
1. Fetch the full PR diff
2. Optionally fetch the latest Argus review from DB for this PR
3. Send to LLM with summary prompt
4. Post as a top-level PR comment via `ghClient.PostComment()`

**LLM Prompt:**
```
Summarize this pull request at a high level for a team standup.
PR #42: "Add user authentication" by @alice

<diff>

Argus review findings (if available):
<previous review summary>

Write a 3-5 sentence summary covering: what changed, why it matters, any risks.
```

### 7.6 Diagram (`@argus diagram`)

**Flow:**
1. Fetch the full PR diff
2. Send to LLM with diagram prompt
3. Post Mermaid diagram as a PR comment (wrapped in a ```mermaid code block)

**LLM Prompt:**
```
Analyze the code flow in this pull request and generate a Mermaid sequence diagram.
Focus on the main execution path introduced or modified by the changes.

<diff>

Output ONLY a valid Mermaid sequence diagram. No other text.
Example format:
sequenceDiagram
    participant A
    participant B
    A->>B: Request
    B-->>A: Response
```

---

## 8. Orchestrator Changes

### 8.1 New Method: `HandleCommentEvent`

```go
func (o *Orchestrator) HandleCommentEvent(ctx context.Context, event github.CommentEvent) error {
    // Only process "created" actions (ignore edits/deletes)
    if event.Action != "created" {
        return nil
    }

    // Parse command from comment body
    cmd := ParseCommand(event.CommentBody)
    if cmd == nil {
        return nil // no @argus mention or unrecognized command
    }

    owner, repo, err := splitRepoFullName(event.RepoFullName)
    if err != nil {
        return err
    }

    // Acknowledge with eyes reaction
    _ = o.ghClient.AddReaction(ctx, event.InstallationID, owner, repo, event.CommentID, "eyes")

    // Log activity
    _ = o.st.LogActivity(ctx, "comment_command", event.CommentAuthor, event.RepoFullName,
        []byte(fmt.Sprintf(`{"command":"%s","pr":%d}`, cmd.Kind, event.PRNumber)))

    // Dispatch
    var handlerErr error
    switch cmd.Kind {
    case CmdReReview:
        handlerErr = o.handleReReview(ctx, event, cmd)
    case CmdResolve:
        handlerErr = o.handleResolve(ctx, event)
    case CmdCreateIssue:
        handlerErr = o.handleCreateIssue(ctx, event, cmd)
    case CmdDescribe:
        handlerErr = o.handleDescribe(ctx, event)
    case CmdSummarize:
        handlerErr = o.handleSummarize(ctx, event)
    case CmdDiagram:
        handlerErr = o.handleDiagram(ctx, event)
    }

    // Update reaction based on result
    if handlerErr != nil {
        _ = o.ghClient.AddReaction(ctx, event.InstallationID, owner, repo, event.CommentID, "confused")
        return handlerErr
    }
    _ = o.ghClient.AddReaction(ctx, event.InstallationID, owner, repo, event.CommentID, "rocket")
    return nil
}
```

### 8.2 LLM-Based Handlers

`handleDescribe`, `handleSummarize`, and `handleDiagram` all follow the same pattern:
1. Fetch diff
2. Build prompt
3. Call LLM
4. Post result to GitHub

These use a new LLM pipeline stage config: `llm.StageInteraction` — allowing users to configure a different model for interactive commands vs. reviews (e.g., faster/cheaper model for summaries).

---

## 9. Database Changes

### 9.1 No Schema Changes Required

The existing tables support this feature:
- `reviews` table: re-review creates a new row with `trigger: 'comment'` and `triggered_by: '<username>'`
- `activity_log`: tracks all command invocations
- `repos` + `installations`: used for lookup and auth

### 9.2 Optional: Command Log Table (Phase 2)

If we want analytics on command usage:

```sql
CREATE TABLE comment_commands (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repo_id     BIGINT REFERENCES repos(id),
    pr_number   INT NOT NULL,
    command     TEXT NOT NULL,
    triggered_by TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending', -- pending/completed/failed
    error       TEXT,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);
```

Not required for Phase 1 — activity_log is sufficient.

---

## 10. GitHub App Permissions

The GitHub App needs these additional permissions:

| Permission | Current | Needed | Why |
|---|---|---|---|
| Issues | none | Read & Write | Create issues |
| Pull requests | Read & Write | Read & Write | Already have |
| Metadata | Read | Read | Already have |

Webhook event subscriptions to add:
- `issue_comment`
- `pull_request_review_comment` (may already be subscribed)

---

## 11. Implementation Phases

### Phase 1: Core Infrastructure + Re-Review + Resolve
- [ ] `CommentEvent` type and parsers in `webhooks.go`
- [ ] `ParseCommand()` regex parser in `pipeline/commands.go`
- [ ] Webhook handler cases in `server.go`
- [ ] `HandleCommentEvent()` in orchestrator
- [ ] `PostComment`, `AddReaction`, `GetPR` in `client.go`
- [ ] Re-review handler (reuses `HandlePREvent`)
- [ ] Resolve handler + GraphQL thread resolution
- [ ] Update GitHub App permissions + webhook subscriptions

### Phase 2: LLM-Powered Commands
- [ ] `StageInteraction` model config
- [ ] `handleDescribe` + `UpdatePRBody` client method
- [ ] `handleSummarize` + summary prompt
- [ ] `handleCreateIssue` + `CreateIssue` client method + LLM for structuring
- [ ] `handleDiagram` + Mermaid prompt

### Phase 3: Polish
- [ ] Rate limiting (prevent spamming re-review)
- [ ] Cooldown per PR per command (e.g., max 1 re-review per 5 min)
- [ ] Per-repo command enable/disable via `settings_json`
- [ ] `comment_commands` analytics table
- [ ] Bot name auto-discovery via `GET /app`

---

## 12. File Change Summary

| File | Changes |
|------|---------|
| `internal/github/webhooks.go` | Add `CommentEvent`, `ToIssueCommentEvent`, `ToPRReviewCommentEvent`, update `extractAction` |
| `internal/github/client.go` | Add `PostComment`, `AddReaction`, `ResolveReviewThreads`, `CreateIssue`, `UpdatePRBody`, `GetPR`, `ListPRReviewComments` |
| `internal/api/server.go` | Add `issue_comment` and `pull_request_review_comment` cases in `handleWebhook` |
| `internal/pipeline/commands.go` | New file: `CommandKind`, `Command`, `ParseCommand()` |
| `internal/pipeline/orchestrator.go` | Add `HandleCommentEvent`, per-command handlers |
| `internal/pipeline/interactions.go` | New file: LLM prompts and handlers for describe/summarize/diagram |
| `internal/pipeline/states.go` | (Optional) Add `StageInteraction` if using model config |
| `internal/llm/registry.go` | Add `StageInteraction` constant |

---

## 13. Unresolved Questions

- Bot name: hardcode `argus` or discover dynamically via `GET /app`? Dynamic is more correct but adds startup complexity
- Resolve threads: use `go-github` GraphQL or raw HTTP? Only one mutation needed, raw HTTP is simpler
- Re-review scope: support file-scoped re-review in Phase 1 or defer?
- Rate limiting strategy: in-memory (lost on restart) or DB-backed?
- Should `@argus help` list available commands?
- Should LLM-generated PR descriptions replace the entire body or append a section?
- Create issue: auto-assign labels or let user specify?
- Diagram: sequence diagram only, or support other Mermaid types (flowchart, class diagram)?
