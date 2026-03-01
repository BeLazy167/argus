# Token Usage Tracking + Comment Reply Re-analysis

## Goal

Track token usage and cost per review. Allow Argus to respond to replies on its review comments — resolving, clarifying, or standing firm — and learning from feedback.

## Feature 1: Token Usage & Cost

### Architecture

Token data already flows through `CompletionResponse.TokenUsage` but is discarded. DB column `token_usage JSONB` exists on `reviews` table but is unused.

### Changes

| File | Change |
|------|--------|
| `internal/llm/provider.go` | Add `Cost float64` to `CompletionResponse` |
| `internal/llm/chat.go` | Parse OpenRouter cost from response body |
| `internal/pipeline/types.go` | Add `TokenUsage` accumulator to `PipelineRun`, per-stage breakdown |
| `internal/pipeline/triage.go` | Accumulate triage tokens into `PipelineRun` |
| `internal/pipeline/review.go` | Accumulate review tokens (per-file, summed) into `PipelineRun` |
| `internal/pipeline/orchestrator.go` | Serialize accumulated tokens as JSONB, pass to `UpdateReview` |
| `internal/store/models.go` | Add `TokenUsage *json.RawMessage` to `Review` |
| `internal/store/queries.go` | Include `token_usage` in INSERT/SELECT for reviews |
| `web/src/lib/types.ts` | Add `TokenUsage` interface, add to `Review` |
| `web/src/app/(dashboard)/reviews/[id]/page.tsx` | Token pill next to score: `14.2k tokens · $0.008` with hover breakdown |
| `web/src/app/(dashboard)/reviews/page.tsx` | Tokens column in list |

### Cost Source

OpenRouter returns cost in response body. Use when available, show tokens-only otherwise.

---

## Feature 2: Comment Reply Re-analysis

### Flow

1. GitHub fires `pull_request_review_comment` webhook (action: `created`) when someone replies
2. Check `in_reply_to_id` against stored `github_comment_id` to verify it's a reply to an Argus comment
3. Fetch: original Argus comment + human reply + file diff context
4. LLM decides: `resolve` | `clarify` | `stand_firm`
5. Execute via GitHub API (post reply, optionally resolve thread)
6. If learning extracted, index in Supermemory

### No reply depth limit

LLM decides when to disengage.

### Changes

| File | Change |
|------|--------|
| `internal/store/migrations/005_comment_reply_support.sql` | Add `github_comment_id BIGINT` + index to `review_comments` |
| `internal/store/models.go` | Add `GithubCommentID *int64` to `ReviewComment` |
| `internal/store/queries.go` | Store `github_comment_id` when creating comments, add `GetCommentByGithubID` |
| `internal/github/webhooks.go` | New `CommentEvent` struct |
| `internal/api/server.go` | Handle `pull_request_review_comment` event |
| `internal/pipeline/reply.go` | **New** — `ReplyAnalyzer` with LLM prompt, action execution, memory indexing |
| `internal/pipeline/orchestrator.go` | Store `github_comment_id` after posting comments to GitHub |

### GitHub App Settings

Enable at `github.com/settings/apps/argus-eye`:
- **Pull requests**: Read & Write
- **Issues**: Read & Write
- **Subscribe**: Pull request review comment, Pull request review thread

### LLM Prompt (reply.go)

Returns structured JSON:
```json
{
  "action": "resolve|clarify|stand_firm",
  "reply": "response text",
  "learning": "optional pattern to remember"
}
```
