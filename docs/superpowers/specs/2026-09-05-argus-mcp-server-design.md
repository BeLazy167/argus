# Argus MCP Server — Design

Status: implemented — see docs/superpowers/plans/2026-09-05-argus-mcp-server.md
Date: 2026-09-05

## Problem

Argus accumulates two bodies of knowledge that only Argus can read today: the
memory corpus (patterns, conventions, feedback, with embeddings) and review
results (findings, scores, diagrams, learned memories). A team member working in
their own agent has no way to reach either. They re-ask questions the corpus
already answers, and they check review status by opening the dashboard.

Expose both over MCP, scoped per user, so any team member's agent can search the
team's memory, contribute to it, and read reviews.

## Decisions

| Decision | Choice | Why |
|---|---|---|
| Where it runs | Inside the existing Go backend at `/mcp` | Auth, tenancy, store, indexer registry, config and deploy already exist. A separate service re-implements all of them. |
| Transport | Streamable HTTP, `Stateless: true` | Two Fly machines with no session affinity. |
| Identity | Clerk as OAuth authorization server | Clerk officially supports the MCP authorization-server role. No new credential store. |
| Organization scope | One org selected during OAuth authorization | Membership in other orgs does not grant this connection access to them. Switching orgs requires another authorization. |
| Access level | Read + write memory, read reviews | Reviews are read-only: retry/cancel are multi-stage orchestrations that double-post to GitHub if short-circuited. |
| Write permission | Every member of the selected org | No maintainer role or original-author restriction. Confirmation flags acknowledge an operation; they do not prove human approval or grant access. |
| Destructive writes | Human-authored rows only, confirm flag to override | Pipeline-learned knowledge represents months of review learning and there is no un-invalidate. |
| Org-wide writes | Allowed behind an explicit confirm flag | Org-wide docs carry a pinned `confidence=1.00` and reach every repo in the installation. |
| Forgetting knowledge | Retire the memory | Deleting a pattern removes one contributing record and may leave the memory searchable. |
| Duplicate writes | Check first; reuse an exact match atomically | Retries must not create extra contributing patterns. Similar content requires acknowledgment before insertion. |
| Rollout | Config-gated route | Deploy dark; kill instantly without a rollback. |

## Architecture

### Mount point

`/mcp` registers as a sibling of the webhook and stream routes inside
`NewServer` (`backend/internal/api/server.go:77`), **not** inside
`r.Route("/api/v1", ...)`. Follow the `registerPprofRoutes` shape
(`backend/internal/api/handlers_pprof.go:26`), including the config-gated early
return that leaves the surface at 404 when disabled.

```go
r.Route("/mcp", func(r chi.Router) {
	r.Use(s.mcpAuthChallenge)            // NEW: OAuth challenges
	r.Use(s.mcpAuth)                     // NEW: validated OAuth claims
	r.Use(s.requireMCPInstallationScope) // NEW: selected org only
	r.Handle("/", s.mcpHandler)
})
```

Mounting outside `/api/v1` avoids that group's `middleware.Timeout(60s)`
(`server.go:166`). The HTTP server's `WriteTimeout` still applies.

`requestLogging` is global. It streams incoming request bodies and tees response
bodies (`middleware_logging.go:60-78`), including routes outside `/api/v1`.
N6 must exclude both body directions for `/mcp` and `/mcp/`, including rejected
requests and errors. Keep sanitized request metadata, status, and duration.
Queries, proposed memory content, and returned memories must never enter the
HTTP payload log. The existing webhook exception covers request bodies only.

### Request path

1. `traceIDMiddleware` → `RequestID` → `RealIP` → `requestLogging` →
   `panicRecovery` → `cors` (`server.go:122-129`).
2. N5 `s.mcpAuth` reuses the JWT verification implementation behind
   `validateToken` (`middleware.go:137`), extended with the MCP token policy
   below. It carries verified user, selected org, and granted scopes.
3. N5 `s.requireMCPInstallationScope` resolves only the selected org's local
   installation. It rejects missing org claims and conflicting installation
   hints before calling the existing org-resolution logic. It never takes the
   user-wide fallback in `resolveInstallationIDs` (`middleware.go:193`).
4. `mcp.NewStreamableHTTPHandler(getServer, opts)` with
   `opts = &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}`.
5. `getServer(r *http.Request) *mcp.Server` reads `getUserID(r.Context())` and
   `getInstallationIDs(r.Context())` into a plain value, then registers tools
   whose handlers **close over it**.
6. Each tool checks its required OAuth scope and authorizes its id arguments
   before use.

### OAuth configuration and permission policy

V1 accepts Clerk OAuth access tokens in JWT format. Configure the OAuth client
as public with PKCE and keep its consent screen enabled. Support dynamic client
registration for clients that discover the server without a preconfigured
client ID. Opaque tokens and dashboard session tokens are not MCP credentials.

Request `user:org:read` so the user selects an org during consent. A verified,
nonempty `org_id` is required on every authenticated MCP request. A connection
authorized for org A cannot use an installation or repo from org B, even if its
user belongs to both. An `X-Installation-ID` header cannot select another org.
Every member of the selected org may change that org's memory; there is no
additional role or authorship check.

Define and assign the custom scopes `argus:read` and `argus:memory:write` in
Clerk, and advertise them in discovery metadata. Read tools require
`argus:read`; create, delete, and retire require `argus:memory:write`. Configure
the default consent scopes for clients that omit `scope` to include these two
scopes and `user:org:read`. Explicitly narrower grants remain narrower: a
read-only token cannot mutate memory. Copy granted scopes into `tenantScope`
and check them in tool handlers. Flags cannot substitute for either org
membership or a granted write scope.

Configure `CLERK_ISSUER_URL` and `MCP_RESOURCE_URL`, the canonical public HTTPS
URL ending in `/mcp`. Protected-resource metadata identifies that resource and
the Clerk authorization server; challenges point to that metadata. Verify the
signature, issuer, resource audience, expiration, applicable not-before claim,
user subject, and access-token type before trusting org or scope claims. Reuse
the existing verifier's cryptographic code; add an MCP-specific policy rather
than silently broadening the dashboard policy. Do not derive the expected
issuer or audience from request headers. Reject a token without the required
resource audience; never work around a failed client integration by disabling
audience verification.

Clerk documents JWT access tokens, org selection through `user:org:read`, and
explicit enforcement of custom scopes in its
[OAuth configuration guide](https://clerk.com/docs/guides/configure/auth-strategies/oauth/how-clerk-implements-oauth).
The end-to-end acceptance test below must verify the configured token claims
and client flow before enabling the route in production.

### Tenant scope lives in a closure, never in the handler's context

`getServer` is the only place with access to the `*http.Request`; tool handlers
receive `*mcp.CallToolRequest`, which has no HTTP request. Scope is therefore
captured by value:

```go
scope := tenantScope{userID: ..., orgID: ..., installationIDs: ..., grantedScopes: ...}
```

This is a correctness requirement, not a style preference. In stateful mode the
SDK binds a session to the *initialize* request's context, so tool handlers would
inherit the first caller's tenant. Stateless mode plus closure capture makes that
structurally impossible.

`getServer` must perform **no I/O** — no `GetIndexer`, no DB reads. It runs on
every request.

### Per-tool authorization

Every tool argument that is an id is validated before use:

- installation ids: `containsID(scope.installationIDs, in.InstallationID)`
  (`server.go:342`).
- repo ids: `store.GetRepoScoped(in.RepoID, scope.installationIDs)`
  (`queries.go:616`), then take the tenant from `repo.InstallationID` — never
  from the request.

No tool infers a tenant from `scope.installationIDs[0]`. Existing REST middleware
silently ignores malformed or non-member `X-Installation-ID` values
(`middleware.go:229-239`). The MCP wrapper must reject them. Its installation
set comes only from the org selected during OAuth authorization, and every
explicit id is checked against that set.

## Tool set

Nine tools. Repo, installation, and pattern ids are local database ids, never
GitHub ids. `custom_id` is the deterministic memory identity, scoped to an
installation. Search results expose these identities by name.

### Discovery

**`list_repos`** — the id-resolution entry point for every other tool.

- Input: none.
- Output: `[{repo_id, full_name, installation_id, enabled, default_branch}]`.
- Calls: `store.ListReposScoped(ctx, installationIDs)` (`store/queries.go:589`) — two
  parameters, no limit/offset. Precedent: `(*Server).listRepos` (`server.go:175`).

It exists because three surfaces disagree about identifiers: `MemoryQuery.Repo`
takes the short repo name (`memory/reader.go:23-44`), memory tenancy takes the
local installation id, and review detail carries no `full_name` at all.

### Memory — read

**`search_memory`** — hybrid vector + full-text search over one installation.

- Input: `repo_id` (required unless `scope="shared"`), `installation_id`
  (required only when `scope="shared"`), `query` (required),
  `scope enum{repo,shared,both}` default `repo`,
  `type enum{pattern,scenario,trace,feedback,synthesis,pr_summary,review,topology,rule}?`,
  `limit` default 10 clamp 1..25, `threshold` default
  `memory.NewThresholds().FindingEnrich` (0.70, `memory/thresholds.go:146`).
- Output: `{installation_id, matches: [{custom_id, pattern_ids: [], content,
  score, type, source, container_tag, written_at?, metadata?}],
  embeddings_available, truncated}`.
- Calls: `s.resolveIndexer` → `Registry.GetIndexer` (`memory/registry.go:93`)
  → `Indexer.Search` (`memory/indexer.go:81`). Container tags derive from
  `GetRepoScoped` → `SplitN(full_name, "/", 2)[1]` → `memory.RepoTagNew`
  (`memory/tags.go:80`); shared uses the `memory.SharedTag` constant `"_shared"`
  (`memory/tags.go:49`), with the
  tag-allowlist idiom from `pipeline.ToolHandler.tagAllowed`
  (`pipeline/tools.go:170`).

Default `scope="repo"`. `ScopeBoth` fails whole when one leg fails
(`reader.go:137`), and `Search` owns a non-configurable 5s internal timeout.

The PG search projection returns `custom_id` as `PatternMatch.ID`
(`pgsearch.go:278,381`). N9 resolves the returned identities to all associated
local pattern ids in one installation-scoped batch lookup, ordered by pattern
id. An empty `pattern_ids` array means this memory has no contributing pattern
row; it can still be retired. A lookup failure is a tool error, not an empty
array. Do not use `GetPatternIDByCustomID` with `LIMIT 1` for this mapping:
multiple sibling patterns can own the same identity. Never silently truncate
the sibling mapping or interpret `custom_id` as a numeric pattern id.

Raw `Filters` and `PointLookup` are **not** exposed. `PointLookup` licenses a
predicate scan with no score gate (`pgsearch.go:328-346`), and malformed numeric
or `array_contains` filters compile to `false` and silently return nothing
(`pgsearch.go:466-560`).

**`get_memory_briefing`** — the assembled markdown briefing for a repo.

- Input: `repo_id`, `query`, `file_path?`,
  `profile enum{specialist,review}` default `review`, `char_cap?`.
- Output: `{markdown, empty}` — `""` is a legitimate result
  (`briefing.go:104-107`).
- Calls: `Indexer.Briefing` (`indexer.go:88`, impl `pgsearch.go:629`).

`Briefing` runs `OpenConventionConflicts` first and hard-fails the whole briefing
if that query errors (`pgsearch.go:629-640`); surface that as a tool error.

### Memory — write

**`create_memory`** — write a human-authored memory.

- Input: `installation_id` (required), `repo_id?`, `content` (required, cap 4000
  chars), `category?`, `shared` default false, `confirm_shared` default false,
  `confirm_duplicate` default false.
- Output is a discriminated result:

| `status` | Fields | Effect |
|---|---|---|
| `created` | `pattern_id`, `custom_id`, `scope`, `mirror_state:"pending"` | A new pattern and outbox event committed. |
| `existing` | `pattern_id`, `custom_id`, `scope` | An exact match already exists; nothing changed. Searchability is not asserted. |
| `confirmation_required` | `similar:[{custom_id, content, score}]` | Similar content exists; nothing written. |

- Calls: N10 atomic create-or-get operation, reusing the transactional insert
  and outbox logic of `store.CreatePattern` (`store/patterns.go:109`), with
  `memoryCustomID` from `memory.PatternCustomID("", repoName, source, content)`
  (`memory/indexer.go:235`) or `memory.SharedPatternCustomID(source, content)`
  (`indexer.go:245`). Check exact identity first, then use `Indexer.Search` for
  near duplicates before attempting a new insert. The exact check must run
  again atomically with insertion, as specified below.

Validate the scope before deriving identity or accessing the indexer:

| `shared` | `repo_id` | Required acknowledgment | Result |
|---|---|---|---|
| false or omitted | Present and belongs to `installation_id` | None | Repo-scoped write |
| true | Absent | `confirm_shared=true` | Shared write |
| false or omitted | Absent | Any | Reject |
| true | Present | Any | Reject |

Reject an installation outside the selected org and a repo outside that
installation. `confirm_shared` never changes scope by itself. The store derives
shared scope from `RepoID == nil`, so missing `repo_id` must not reach it through
the default repo-write path.

Sanitize content before validating its nonempty length and deriving its
identity. An exact duplicate has the same installation and deterministic
`custom_id`. Return the existing pattern, choosing the lowest pattern id if
legacy siblings exist, without changing its author, queuing another mirror
event, or reviving retired knowledge. `confirm_duplicate=true` does not override
exact deduplication.

For a new identity, search the target container using the sanitized content,
type `pattern`, limit 5, and the configured `FindingEnrich` threshold. If there
are similar matches and `confirm_duplicate=false`, return
`confirmation_required`. The caller can acknowledge those matches and retry
with the flag. A failed similarity search or unavailable embedder is an
explicit error unless the caller acknowledges proceeding without the check
with `confirm_duplicate=true`. Confirmed retries still run exact deduplication.
Similarity is advisory and sees only mirrored memories; it cannot guarantee
the absence of concurrently created near duplicates.

N10 must serialize the final exact-identity check and insert across processes.
A handler-level check followed by `CreatePattern` is insufficient: two callers
can both observe no row. Use a transaction with a shared identity-lock protocol
for pattern creation paths, including dashboard and pipeline writers, and
recheck after acquiring the lock. MCP and dashboard manual creation return the
existing row for an exact match. Preserve existing sibling rows and other
sources' contribution policies; do not add a uniqueness constraint that
invalidates them. Hold no database lock while doing embedding or
similarity-search network work.

`source` is `"manual"` — the same identity namespace the dashboard uses
(`handlers_patterns.go:124`), **not** a new `"mcp"` value. `source` is hashed
into `custom_id`, so a distinct source silently forks identity and leaves two
live copies of the same sentence that never dedup.

Authorship travels in `mirrorExtra`:
`{"created_by": <clerk sub>, "origin": "mcp"}`. This is the only channel that
carries authorship into the memory row; both existing human write paths pass
`nil` and lose it.

Writing org-wide (`shared=true`, no `repo_id`) requires `confirm_shared=true`.
This is an acknowledgment by the caller, not evidence of separate human approval.
Org-wide documents are built with a pinned `confidence=1.00`
(`memory/docbuild.go:118-135`) and influence every repo in the installation.

**`delete_memory`** — permanently remove one contributing pattern row.

- Input: `pattern_id`, `confirm_pipeline_learned` default false.
- Output: `{pattern_deleted:true, custom_id, source, siblings_at_delete,
  retention_expected, mirror_state:"pending"}`.
- Calls: `store.GetPattern` (`store/patterns.go:281`, **unscoped** — check
  `pattern.InstallationID` with `containsID` exactly as
  `handlers_patterns.go:80-84` does), then N2 extends the transactional
  `store.DeletePattern` operation (`store/patterns.go:180`) to return the
  deletion identity and remaining sibling count.

Refuses unless `pattern.Source ∈ {manual, remember_command}` or
`confirm_pipeline_learned=true`. `DeletePattern` itself checks neither source nor
author.

Count remaining patterns for the same installation and effective memory
identity inside the deletion transaction, after removing the selected row.
`siblings_at_delete` describes that snapshot; `retention_expected` is whether
the count is positive. Neither field reports the memory's observed state or
guarantees the worker's later decision. Concurrent writes can change ownership.
The source guard must be enforced against the row being deleted in that
transaction, not solely against an earlier handler read.

Even with zero siblings, the memory can remain searchable until the mirror
worker processes the deletion. Use `retire_memory` when the intent is to stop
retrieval. Tool descriptions must explain this distinction.

**`retire_memory`** — stop a memory being retrieved; optionally point at its
replacement.

- Input: `installation_id`, `custom_id`, `replaced_by_custom_id?`, `reason`
  (required), `confirm_pipeline_learned` default false.
- Output: `{mode enum{invalidated,superseded}, custom_id, source, replaced_by?}`.
- Calls: N8, an installation-scoped guarded retirement operation in the memory
  package. Reuse the transition SQL of `SupersedeDocument`
  (`pgindexer.go:909`) and `InvalidateDocument` (`pgindexer.go:888`) within its
  transaction. The tool must not call those unguarded methods directly.

After validating the installation, load and lock the actual, non-deleted
`memories` row by `(installation_id, custom_id)`. Read its source from stored
metadata, including for memories with no `patterns` row. Never trust a source
supplied by the caller or inferred from the custom id. Perform the source check
and lifecycle transition in the same transaction so a concurrent metadata
change cannot bypass the guard.

Allow `manual` and `remember_command` without an override. Other recognized
sources require `confirm_pipeline_learned=true`. Missing or unrecognized source
metadata is an explicit error even with the flag; resolve provenance before
retiring that row. A missing or deleted memory returns "not found or not
accessible". Repeating an already completed retirement is idempotent after the
same source check.

A replacement must be a distinct live memory in the same installation. Validate
and protect its live state in the retirement transaction. A missing, retired,
or cross-installation replacement fails without invalidating the source. This
also covers a replacement whose create event has not yet been mirrored.

One tool rather than two. Supersede is invalidate plus a forward pointer;
splitting them invites a caller to invalidate first and then fail to link,
leaving knowledge dead with no successor.

Custom ids are not globally unique — `PatternCustomID` discards the owner
(`indexer.go:234-249`) — so the installation is validated separately from the id.

### Review — read

**`list_reviews`** — find review ids.

- Input: `repo_id?`, `limit` default 20 clamp 1..100, `offset` clamp ≥ 0.
- Output: `[{review_id, repo_id, pr_number, pr_title, status, score,
  deep_review, is_incremental, created_at, completed_at}]`.
- Calls: `store.ListReviewsScoped` (`queries.go:2108`) or
  `store.ListAllReviewsScoped` (`queries.go:2141`). Both are self-authorizing —
  the installation filter is in the SQL.

Project only the listed fields. List rows are not detail rows: `token_usage`,
`brief` and `review_contract` are absent, and `diagrams`/`truncated_files` are
the synthesized literal `"[]"` (`queries.go:2100-2107`).

**`get_review_status`** — cheap poll for a running or finished review.

- Input: `review_id`.
- Output: `{review_id, status, stage?, attempt_generation, score?, error?,
  duration_ms?, created_at, completed_at?, repo_full_name, pr_number,
  github_pr_url}`.
- Calls: `store.GetReview` (`queries.go:641`) → `store.GetRepoScoped`
  (`queries.go:616`, the authz gate) → `store.GetReviewAttemptGeneration`
  (`queries.go:976`) → **N1** `GetLatestRunStateForReview`.

`stage` comes from `pipeline_states.state` and is never derived from `status`:
the vocabularies differ (5 values vs 13, `pipeline/states.go:6-24`). If N1 is
cut, omit `stage` rather than fake it.

**`get_review`** — everything the dashboard review page shows, in one call.

- Input: `review_id`, `include_suppressed` default false.
- Output: `{review, repo_full_name, github_pr_url, findings[],
  suppressed_findings[], minor_notes[], history[], auto_resolve_events[],
  memories[], memory_counts[], counts:{total, by_severity, by_file},
  degraded_sections[]}`.
- Calls, in the order `getReview` uses (`handlers_reviews.go:61-130`):
  `GetReview` → `GetRepoScoped` → `GetReviewComments` (`queries.go:665`) →
  `GetReviewMinorNotes` (`queries.go:2221`) → `ListPRReviewSummaries`
  (`queries.go:736`) → `ListPRAutoResolveEvents` (`queries.go:774`) →
  `ListReviewMemories` (`store/memories.go:83`, tenant =
  `repo.InstallationID`) → `CountReviewMemoriesByType` (`memories.go:129`).

Three deliberate differences from the REST handler:

1. `repo_full_name` and the GitHub PR URL come from the authz check, saving the
   dashboard's second round-trip to `/api/v1/repos`.
2. The four sidecars that `getReview` degrades to `nil` on error
   (`handlers_reviews.go:100-119`) are named in `degraded_sections[]`. Serialized
   as bare `null`, a failed query is indistinguishable from a genuinely empty
   one, and an agent would report "no findings" for a failure.
3. Findings are partitioned on `state == "suppressed"` before counting, so counts
   describe what the caller actually received.

`memory_counts` comes from `CountReviewMemoriesByType`, never `len(memories)` —
the list is capped at 50 with 280-char excerpts (`memories.go:20,25`).

`GetReviewComments` is filtered to the current `attempt_generation`, so a retried
review's earlier findings are not returned; the output states the generation.

Do not promise `simulation_results` or `file_count`: the columns exist but
`store.Review` never serializes them.

### Rejected tools

| Rejected | Why |
|---|---|
| `retry_review`, `cancel_review` | Retry is a 5-stage orchestration (GitHub reconciliation → `EnsureNotRunning` → admission gate → in-flight slot → `BeginReviewRetry`, `handlers_reviews.go:421-552`). Skipping any step double-posts to the PR. |
| `export_review` | No authenticated route exists — only `exportReviewPublic` (`server.go:145`), which skips tenancy (`handlers_reviews.go:161`). `get_review` is a superset except dropped findings. |
| `stream_review` | Transport mismatch, 60s `WriteTimeout` (`app.go:345`), 500-event replay pages. `get_review_status` polling covers the need. |
| `update_memory` | There is no edit. `custom_id` is `sha256(normalizeBody(content))`, so an edit forks the corpus and leaves both copies live. The sequence is create → retire(old, replaced_by=new) → delete(old), with a wait for the mirror between. |
| `list_memories` | `search_memory` + `get_memory_briefing` cover browsing; the existing handler is a plain SQL listing with no similarity (`handlers_memories.go:127`). |
| `reembed_memories` | `ReembedCurrentSpace` takes a cross-machine advisory lock and holds pool connections for the whole run (`registry.go:334`). Must never be model-callable. |
| `list_installations` | `list_repos` already returns `installation_id` per row. |

## Indexer access

`Server.memRegistry` already exists (`server.go:71`), wired at `app.go:333`, and
is used today only for `ReembedCurrentSpace` and `InvalidateEmbedder`. Resolve
it through an unexported helper mirroring `orchestrator.resolveIndexer`
(`pipeline/orchestrator.go:232-238`). N8 separately adds the guarded retirement
operation to the indexer seam.

```go
func (s *Server) resolveIndexer(ctx context.Context, installationID int64) memory.Indexer {
	if s.memRegistry == nil { return nil }
	return s.memRegistry.GetIndexer(ctx, installationID)
}
```

Rules:

- Call it inside the tool handler, **after** the tenant check — never in
  `getServer`. Each call costs an uncached `installations` read for
  `disable_shared_decay` (`registry.go:108`) plus a possible embedder resolve
  with an AES decrypt (`store/provider_keys.go:191-208`).
- `GetIndexer` returns a literal `nil`, never an error, when the Postgres backend
  was never wired (`registry.go:93-103`). Nil-check and return an explicit
  "memory backend unavailable" tool error. Never return an empty result set for
  this state.
- Do not call `ForReview` (`pgindexer.go:96`). MCP writes are not attributable to
  a review run.
- Depend on a narrow seam for testability:
  `type memoryRegistry interface { GetIndexer(context.Context, int64) memory.Indexer }`,
  as `pipeline.reactionMemoryRegistry` does (`pipeline/reactions.go:36`).

## Write semantics

| Verb | Effect | Reversible |
|---|---|---|
| `create` | Inserts a `patterns` row and an outbox event; the memory row appears when the mirror worker drains | n/a |
| `delete` | Removes one `patterns` row and queues an outbox event; the worker later soft-deletes the memory unless a sibling still owns the identity | No |
| `invalidate` | Row stays; becomes unretrievable by every reader | **No un-invalidate exists** |
| `supersede` | Invalidate plus a forward pointer to the replacement | No |

New `create_memory` writes return `status:"created", mirror_state:"pending"`.
Exact duplicates return `status:"existing"` without another write. Neither
result asserts searchability. A new relational write commits immediately;
searchability follows the outbox. A poison outbox event stalls every subsequent
pattern mirror for that installation while
writes keep returning success (`memory_mirror_outbox.go:223-237`, retry backoff
to 1h at `:637-649`).

`delete_memory` returns `pattern_deleted`, `siblings_at_delete`,
`retention_expected`, and `mirror_state:"pending"`. These distinguish a
committed pattern deletion from eventual memory removal. There is no
`memory_row_retained` boolean: the tool does not observe the worker's final
outcome. A stalled mirror can leave even the last contribution searchable.
Results never use the word "forgotten". Retirement is the normal operation
when a user wants knowledge excluded from retrieval.

`retire_memory` requires a `reason`, logged through `op.Outcome`
(`operation_logging.go:23-51`).

Content is scrubbed of NUL bytes before validation, identity derivation,
duplicate checks, and insertion. jsonb rejects `\x00`
with SQLSTATE 22P05, the failure class that previously stranded pipeline runs
permanently.

## New work

Everything else reuses existing symbols.

| Id | Work | Reason |
|---|---|---|
| N1 | `store.GetLatestRunStateForReview(ctx, reviewID) (string, error)` | Pipeline stage is exposed only over the WebSocket today; `GetLatestRunForReview` (`queries.go:3790`) returns only the run id. |
| N2 | Transactional source guard, identity, and sibling snapshot in the delete result | Returns `siblings_at_delete` and `retention_expected` after deletion in the same transaction, with `mirror_state:"pending"`; does not assert completed memory removal. |
| N3 | `(*Registry).EmbedderAvailable(ctx, installationID) bool` | With the embedder unavailable, every positive-threshold search returns `(nil, nil)` with one Warn (`pgsearch.go:286-313`) — "unconfigured" is otherwise indistinguishable from "no matches". |
| N4 | Warm the vector probe at startup | `usesPGContextVector` is a process-wide `sync.Once` latched by the first search using *that caller's* context (`pgindexer.go:128`, `pgsearch.go:209`). A short-deadline MCP call latches it wrong and breaks search for the whole process until restart. |
| N5a | `GET /.well-known/oauth-protected-resource` (RFC 9728), `CLERK_ISSUER_URL`, and `MCP_RESOURCE_URL` | Advertise the canonical resource, Clerk authorization server, and supported scopes. Configure public PKCE clients, consent, JWT access tokens, and dynamic registration in Clerk. |
| N5b | `s.mcpAuthChallenge` and `s.mcpAuth` | Add OAuth challenges and an MCP-specific token policy using the existing verifier's cryptographic implementation. Reject session and opaque tokens; use fixed errors. |
| N5c | Issuer, resource audience, temporal claims, subject, token type, and granted-scope validation | Existing `validateToken` checks only RS256, signature and `exp`. Add verified org and scope claims to the MCP request scope; enforce `argus:read` and `argus:memory:write` per tool. |
| N5d | `s.requireMCPInstallationScope` | Require the org selected during consent; reject missing org claims and malformed or conflicting installation hints. Never fall back to all of a user's installations. All members may write. |
| N6 | Exclude both request and response bodies for `/mcp` and `/mcp/` from HTTP logging | The logger is global. Its existing webhook exclusion covers only requests. Test both body directions, including errors and auth failures. |
| N7 | Config flag gating the route | Follows `registerPprofRoutes` (`handlers_pprof.go:26`). |
| N8 | Guarded retirement operation on the indexer seam, with PG transaction and fake implementation | Load stored memory provenance by installation and custom id, enforce the source acknowledgment, validate a replacement, and apply the transition atomically. Works without a pattern row. |
| N9 | Batch identity-to-pattern lookup | Search exposes `custom_id` and every associated `pattern_id`, scoped to the installation; reuse effective identity resolution for legacy rows and sibling accounting. |
| N10 | Atomic manual-pattern create-or-get and shared insertion locking | Serialize the final exact check with insertion across processes and creation paths. Return an existing row for MCP and dashboard manual duplicates; preserve legacy siblings and other sources' contribution policies. |

## Hazards and mitigations

1. **Cross-tenant read or write via an unvalidated id.** Custom ids are not
   globally unique; `GetReview` and `GetPattern` are unscoped. → Validate every
   id argument with `containsID` or `GetRepoScoped`; take the tenant from
   `repo.InstallationID`; allowlist container tags.
2. **`X-Installation-ID` fails silently in REST middleware**, potentially
   returning a wider installation set (`middleware.go:229-239`). → N5d rejects
   invalid hints and missing org claims. No tool infers a tenant from `ids[0]`.
3. **Stateful sessions** would 404 across two machines and leak the initialize
   request's tenant into every tool call. → `Stateless: true`, scope by value.
4. **Request and response bodies leak private content into logs.** → N6.
5. **Irreversible destruction of pipeline-learned knowledge.** → Source
   allowlist plus `confirm_pipeline_learned`, checked against stored provenance
   in the mutation transaction; return the source in the result. Flags are
   acknowledgments, not permission grants or proof of human approval.
6. **Writes that report success and do nothing** (sibling rows block the memory
   delete; `DeleteDocument` no-matches return nil). → Return
   `siblings_at_delete`, `retention_expected`, and `mirror_state:"pending"`.
   Do not infer completed memory removal from zero siblings.
7. **Invalidate is one-way and unprecedented.** → Require `reason`, log the
   outcome, ship behind the config flag.
8. **Eventual consistency**: "saved" then an immediate search finds nothing. →
   `mirror_state: "pending"`; document the delay.
9. **Embeddings off returns an empty list with a nil error.** → N3.
10. **The vector probe latches on the first search.** → N4.
11. **Per-request indexer construction cost**: one `installations` read per call
    plus a periodic cold embedder (5-min TTL, fresh 1024-entry query LRU). →
    Resolve lazily per tool call; measure before adding a cache.
12. **60s write timeout** kills SSE. → Use JSON responses. Mounting outside
    `/api/v1` removes its middleware timeout, not the server write timeout.
13. **Per-request server build can panic**: `mcp.AddTool` panics on an invalid
    schema. → Build one server at boot inside `NewServer` as a schema smoke test;
    use the identical registration function per request.
14. **NUL bytes strand writes or change identity after deduplication.** → Scrub
    before validation and every identity, duplicate, and write operation.
15. **404 conflates unauthorized and missing** (`handleDBError`,
    `server.go:378-385`). → Tool results say "not found or not accessible".
16. **Degraded sections read as facts.** → `degraded_sections[]`; state the
    attempt generation.
17. **Unbounded list limits**; a negative offset errors in Postgres. → Clamp in
    the tool schema.
18. **No audience or issuer validation.** → N5c.
19. **403 bodies echo wrapped DB errors** into the transcript
    (`middleware.go:292`). → Fixed error strings on the MCP path.
20. **Memory content is model-controlled and becomes prompt content.** → Length
    cap, control-character strip, and the repo's standing
    sanitize/wrap/tag-scrub idiom wherever tool output is re-interpolated.
21. **`ScopeBoth` fails whole on one leg.** → Default `scope="repo"`; surface leg
    errors as errors, not as "no results".
22. **Missing `repo_id` silently becomes a shared write.** → Validate the scope
    combinations before the store call; org-wide creation requires both shared
    flags and no repo id.
23. **Check-then-insert races create duplicate contributions.** → N10 serializes
    exact deduplication with insertion. Similarity checks remain advisory and
    run before acquiring a database lock.
24. **Search identities cannot be passed to mutation tools.** → N9 exposes
    explicit custom ids and all contributing pattern ids, including an empty
    list for memories that can only be retired.

## Testing

- **Tool schema smoke test** at boot: build one server during `NewServer` so an
  invalid schema panics at startup, not on a request.
- **Tenant isolation**, per tool: a caller scoped to installation A receives
  "not found or not accessible" for every id belonging to installation B.
  Listings without an id filter exclude B. Cover all nine tools.
- **Selected org**: a user belonging to A and B, authorized for A, cannot read
  or mutate B through any tool or installation hint. Missing org claims and
  malformed or conflicting hints fail. Reauthorizing for B permits B only.
- **Member permissions and scopes**: a regular org member can create, delete,
  and retire another member's memory with a write grant. A read-only token
  cannot write, even with every confirmation flag set.
- **Create scope validation**: exercise every row of the input matrix. Missing
  `repo_id` with omitted flags, shared writes with a repo id, and repo ids from
  another installation must fail before insertion or outbox enqueue.
- **Duplicate creation**: an exact retry returns the same pattern id and adds
  no row or outbox event, including with `confirm_duplicate=true`. Two
  concurrent MCP calls, and concurrent MCP/dashboard manual calls, produce
  one new pattern and one event for a previously absent identity. Existing
  siblings remain untouched; choose the lowest id deterministically. Sanitation
  precedes identity derivation. An exact retry does not revive a retired memory.
- **Similar creation**: matches return `confirmation_required` without a write;
  an acknowledged retry may create a distinct identity. A failed search or
  unavailable embedder is explicit and requires acknowledgment to bypass.
- **Mutation identity mapping**: search a memory with zero, one, and multiple
  contributing patterns. Verify explicit custom ids and all scoped pattern
  ids; delete one sibling without confusing its id with the memory identity.
  The same custom id in another installation never contributes a pattern id.
  Lookup errors must not become empty arrays.
- **Write guards**: delete and retire refuse pipeline-learned sources without
  acknowledgment. Retirement loads memory provenance even without a pattern
  row; missing or unknown provenance fails even with the flag. Verify source
  checks cannot race with a provenance change. Missing, self, retired, or
  cross-installation replacements leave the source unchanged. An unmirrored
  replacement fails explicitly; a repeated completed retirement is idempotent.
- **Deletion timing**: pause the mirror worker, delete the last pattern, and
  assert `pattern_deleted:true`, zero `siblings_at_delete`,
  `retention_expected:false`, and `mirror_state:"pending"` while search still
  returns the memory. Drain the worker and verify removal. Repeat with a
  sibling and verify retention. A concurrent new owner may change the worker's
  eventual decision without making the earlier snapshot a completion claim.
- **HTTP log exclusion**: capture logs for `/mcp` and `/mcp/` using unique
  request and response body markers for search and creation. Include tool
  errors and auth failures. Neither marker may appear; sanitized request
  metadata, status, and duration must remain available.
- **Degradation**: with the memory backend unwired, `search_memory` returns an
  explicit unavailable error, not an empty match list; with the embedder absent,
  it returns `embeddings_available: false`.
- **`get_review` parity**: for a review with all sidecars present, the tool
  returns the same finding, note, history and memory counts the dashboard shows;
  with a sidecar query failing, the section is named in `degraded_sections[]`.
- Follow existing API handler test conventions (`httptest` plus the PG test
  pool); `_pg_test.go` files require `TEST_DATABASE_URL` and the `pgcontext`
  extension from the CI image.

### OAuth end-to-end acceptance

Before production enablement, use a staging Clerk instance and a real MCP
client to complete discovery, dynamic registration, PKCE authorization, org
selection, token exchange, initialization, and `tools/list`. Verify that a
regular member can search, create, and retire a test memory in the selected
org. Record the client version and non-secret configuration used; never record
tokens or private memory content.

Repeat with a read-only grant, a second org, and a user belonging to both orgs.
Assert scope and org isolation as above. In automated verifier tests, reject
wrong issuer, wrong or missing resource audience, expired or not-yet-valid
tokens, missing subject or org, session tokens, and opaque tokens. Assert an
unauthenticated request returns a discoverable OAuth challenge and that scope
denials use fixed errors. The real client token must satisfy the same verifier
policy as the test tokens.

## Out of scope

- Writing web-research results into memory. Monid was used to research this
  design; wiring a research tool that writes findings back into the corpus is a
  separate project with its own design.
- `replace_memory` as a composite (create → wait for mirror → supersede →
  delete). Ships only once the mirror-visibility story is settled; until then the
  three-call sequence is documented, not automated.
- Personal access tokens. Clerk OAuth is the v1 credential; PATs are added only
  if headless or CI use actually demands them.
- Any review mutation.

## Open questions for implementation

1. **Go SDK version.** Pin `go-sdk` v1.4.0 (declares `go 1.24.0`) against the
   current `go 1.24.1` module, or bump the go directive, `.github/workflows/ci.yml`
   and the Dockerfile together. A partial bump passes locally and fails the build
   job.
2. **Rate limiting.** Should MCP tool calls go through `s.rateLimiter` or a
   per-user quota? Every `search_memory` call bills a query embedding on a cache
   miss.
3. **Audit sink.** `Server` carries an `audit` field. Record memory writes and
   retirements there in addition to `patterns.created_by`?
4. **Dropped findings.** They exist only in
   `pipeline_states.payload->AllFileReviews` via `GetAllFileReviewsForReview`
   (`queries.go:3984`) and today reach only the public export. Include them in
   `get_review` behind a flag, or leave them out?
5. **Migration number.** No migration is required by this design. If one is added
   later (e.g. for PATs), take the next free `NNN_` at push time — parallel PRs
   have collided on this.
