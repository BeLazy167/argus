# Memory Hierarchy Design

## Overview

Argus builds institutional memory that gets smarter across all repos under an owner (user or org). Memory follows git-style inheritance: owner-level is the base, repo-level extends/overrides.

## Container Tag Hierarchy

```
{owner}-patterns              ← owner-wide learned patterns
{owner}-rules                 ← owner-wide user rules
{owner}-{repo}-patterns       ← repo-specific patterns (inherits owner)
{owner}-{repo}-rules          ← repo-specific rules (inherits owner)
{owner}-{repo}-reviews        ← repo review history
```

Examples for `qbriad/argus`:
- `qbriad-patterns`, `qbriad-rules`
- `qbriad-argus-patterns`, `qbriad-argus-rules`, `qbriad-argus-reviews`

Tag helpers:
```go
func OwnerTag(owner, kind string) string
func RepoTag(owner, repo, kind string) string
```

## Agentic Memory Retrieval

ContextStage is removed. The review LLM gets `search_memory(query, container_tag)` as a tool-use function backed by `Supermemory.Search()`.

The LLM knows the owner/repo from PR context and picks the right container tag based on what it's reviewing. One tool, AI decides when and what to search.

Pipeline becomes:
```
Triage → Review (with search_memory tool) → Synthesize → Post
```

### Cold-start behavior
New repos have no repo-level memory. The AI naturally falls back to `{owner}-patterns` and `{owner}-rules`, inheriting full org knowledge from day one.

## Promotion (Repo → Owner)

Hybrid auto-promotion. After each review completes, a post-review LLM call decides:

1. **Criticals** → promote to `{owner}-patterns` immediately
2. **Recurring** (same category across 3+ repos or 10+ total occurrences) → promote to `{owner}-patterns`

The LLM generalizes the finding (strips file paths, makes repo-agnostic) before indexing to owner scope via `Supermemory.AddMemory()`.

Metadata on promoted patterns:
```json
{
  "source_repo": "qbriad/argus",
  "promoted_at": "2026-02-27",
  "reason": "critical | recurring",
  "occurrences": 12
}
```

## Cross-Repo Review

The review LLM can reason across repo boundaries by searching other repos' memory.

### Tools

- `search_memory(query, container_tag)` — search any scope (existing)
- `list_repos(owner)` — returns all repos under the owner with inferred roles

### AI-Inferred Topology

After each review, the post-review step infers repo role and dependencies from the code:
- **Role**: frontend, backend, microservice, shared-lib, db, etc.
- **Dependencies**: API consumption, shared imports, proto/schema refs

Stored in `{owner}-patterns` as architectural memory:
```
"qbriad/argus is a Go backend API serving /api/v1/*"
"qbriad/argus-web is a Next.js frontend consuming qbriad/argus API"
"qbriad/argus-db contains Postgres migrations and ent schema"
```

No user config — the architecture map builds itself from reviews over time.

### Cross-Repo Flow

1. LLM reviews a response shape change in `argus` (backend)
2. Searches `{owner}-patterns` for "what repos consume this API"
3. Gets back: "argus-web is a frontend consuming this API"
4. Searches `{owner}-argus-web-reviews` for related past comments
5. Flags: "This response shape change may break argus-web"

## Conflict Resolution

Most specific wins (like git config). Repo-level overrides owner-level.

- System prompt instructs: "When repo-level contradicts owner-level, prefer repo-level."
- Suppression: repo adds a rule to `{owner}-{repo}-rules` saying "ignore owner pattern X". AI sees both, respects suppression.
- No special mechanism — the LLM handles conflicts naturally through context.

## Unresolved Questions

- Max tool-use loops per review? (latency/cost cap)
- Rate-limit on promotion to avoid noisy owner-level memory?
- Should the AI also decide when to *demote* stale owner patterns?
- Token budget allocation: how much context window for memory vs diff?
