# Argus Domain Glossary

Shared vocabulary for Argus. Each term is grounded in the code that implements
it; see [docs/architecture.md](docs/architecture.md) for the full flow. Read
this file before you name a new type — the project already has a word for most
of what a review does.

---

## Admission

These four terms name the decision to start a review. They are one seam, not
four scattered checks.

### Admission

The decision "may this review run". Admission owns four gates: the actor's
permission, the rate limit, the Budget, and the `auto_run` setting. It returns
a Verdict. It holds no locks and keeps no state. It does not know that GitHub
or Clerk exist — the caller resolves an Actor first, then gives Admission plain
values.

### Actor

Who asked for a review. One value, built by two adapters: the GitHub webhook
adapter, and the dashboard JWT adapter.

On the PR-webhook path the actor is the PR author, and the actor is recorded
for **attribution only**. `auto_run` is the authority on that path. Admission
must never consult the actor's permission there. If it did, a fork pull request
would authorize its own review.

### Budget

The limit on what one review may cost. Budget measures two things:

- **PR size** — the file count first, then the changed-line count.
- **Estimated tokens** — the average over the repo's last 20 completed reviews
  (`historicalReviewSampleLimit`, `internal/pipeline/cost_estimator.go`).

Each measure has a soft limit and a hard limit. The most severe answer wins.

### Verdict

The answer Admission returns. It is one of three values, and each one carries a
reason:

| Verdict | Meaning |
|---------|---------|
| `allow` | The review runs at the depth the Review Contract set. |
| `reduce` | The review runs, but reduced: depth drops to one call per file, AND the file count is capped to the highest-risk files. |
| `refuse` | No review runs. The reason goes back to the caller. |

---

## Review pipeline

### Review

One run of the pipeline over one pull request. Persisted as a row in `reviews`
(`store.Review`, `internal/store/models.go`). It carries the status, the head
and base SHA, the trigger, the token usage, the score, the summary, and the
Review Contract it ran under. One pull request can hold many reviews — each
push can add one.

### PipelineRun

The working state of one Review while it executes
(`internal/pipeline/types.go`). It holds the parsed diff, the triage results,
the file reviews, the synthesis, the token counters, and the per-run feature
flags. The state machine persists it to Postgres from the `Reviewing` state
onward, so a restart can resume it.

### Review Contract

The `{change_class, evidence_bar, depth, signals}` computed for every PR before
the pipeline runs (`internal/pipeline/contract.go`), from deterministic metadata
first (draft flag, labels, branch prefix, path globs, size) with an LLM filling
the class only when signals are silent. Gates reviewer routing, Pass 2
eligibility, judge thresholds, and the Glass Box footer.

### Evidence bar

The severity/evidence threshold a finding must clear to be posted, set by the
Review Contract. Raised for throwaway/docs/generated changes; **floored** for
security-relevant files and migrations — a floor no label, branch name, or LLM
classification can relax.

### Persona

A review style, chosen per repo or per organization
(`internal/pipeline/persona.go`). Nine values exist: `default`,
`security_auditor`, `performance_engineer`, `mentor`, `architect`, `strict`,
`adversarial`, `fresh_eyes`, and `custom`. A persona adds an overlay to the
system prompt. It never changes the Review Laws or the severity rubric.
`@argus-eye review --persona <name>` overrides the stored value for one run.

### Specialist

One of four reviewer roles that run in parallel over the diff: `bug_hunter`,
`security`, `architecture`, `regression` (`internal/pipeline/specialists.go`).
Each one gets the same base prompt plus its own focus overlay. Triage and the
Review Contract decide which specialists a file gets. A `one_time_script`
contract replaces the squad with a single balanced reviewer.

### Finding

One issue Argus raises, anchored to a `file:line` and posted as a GitHub review
comment. Persisted in `review_comments`; carries exactly one lifecycle **state**
(see below). The unit everything else in this glossary acts on.

### Blast radius

The set of files that depend on a changed file, up to two hops.
`Store.GetBlastRadius` walks the code graph backwards from the changed paths
(`internal/store/graph.go`), through the pgGraph projection when it is built and
a recursive CTE otherwise. The Validate stage uses it to weight changes to
widely used code. `FileComment.BlastRadius` records the dependent count on the
finding itself.

The walk is bounded by `installation_id`, not `repo_id`. Repositories inside one
installation may depend on each other, so the walk crosses repository lines;
across installations there is no legitimate edge, so it never crosses tenant
lines. `repo_id` still resolves the seeds, because two repositories in one
installation can hold the same file path. `code_nodes.installation_id` is
denormalised from `repos` for this — a pgGraph filter can only read a registered
column of the node table.

A dependent in a sibling repository is **listed but never read**. Every consumer
resolves a dependent's path against this pull request's own repository at its
head SHA, which is the only ref it has, so reading a sibling's path there returns
a different file under the same name. The prompt marks those entries as
belonging to another repository and omits their source.

---

## Memory

### Container tag

The scope of a memory row inside one installation
(`internal/memory/tags.go`). Exactly two shapes exist: `{repo}` for a single
repository, and `_shared` for the whole installation. The memory *type* is a
separate column, not part of the tag. The tenant boundary is `installation_id`,
not the tag — cross-tenant reads are blocked by that predicate on every query.

### customID

The deduplication key of a memory row. Every writer derives it from a content
hash, so a second write of the same content updates the first row instead of
adding another. The store upserts on `(installation_id, custom_id)`. IDs are
truncated to 100 characters.

---

## Re-review and comment lifecycle

### Thread

The GitHub review-comment conversation for a finding. Resolving it clears the
finding from GitHub's require-conversation-resolution merge gate, so resolution is
a privileged action. A finding's thread identity is persisted (ThreadRegistry) so
resolving finding B always targets B's own thread, never a same-line neighbour's.

### Inter-diff

The diff between the **last completed review's** head commit and the new push's
head commit (`GetCompareCommitsDiff`), i.e. only what changed *since the last
review* — not the whole PR diff. The evidence an incremental review and the
addressed-judge reason over.

### Incremental review

A re-review on a `synchronize` push scoped to the inter-diff, with priors carried
across **all** completed reviews on the PR so it can dedup against and verify
prior findings. Resolved once per push as an `IncrementalPlan`
(`internal/pipeline/incremental.go`). Falls back to a full review — with an
`incremental.fallback` signal — on a fetch error, force-push/base-change (empty
compare), or diff parse failure.

### Auto-run

Whether a webhook PR event (opened/synchronize/reopened) triggers a review
automatically. **Default ON** (`IsAutoRunEnabled` resolves an unset flag → on);
a per-repo or per-org `auto_run: false` opts out; a `SELF_HOSTED` deploy always
runs regardless of stored settings. When off, a push posts a one-shot "Trigger
review" affordance instead of silently doing nothing (`decideAutoRun` /
`signalAutoRunDisabled`).

### Auto-resolve

On a `synchronize` push, closing stale finding threads whose anchored lines the
push modified (`autoResolveOnSynchronize`). Proximity is only a cheap prefilter;
the addressed-judge must confirm the fix before a thread resolves. Gated
separately from auto-run (`IsAutoResolveEnabled`, also default ON) and runs even
on manual-review repos — it costs no LLM review spend.

### Addressed (judge-verified)

A finding whose flagged problem the **AddressedJudge** — an LLM-as-judge
(`addressed_judge.go`) — confirmed was actually fixed by the inter-diff, not
merely near a touched or reformatted line. Terminal state `addressed`; the
resolving commit is stamped as the resolved-by-commit breadcrumb
(`resolved_sha`). The judge degrades safe: any error, timeout, or not-addressed
verdict leaves the thread open (never a false resolve).

### Dismissed

The developer rejected the finding (Argus was wrong, or not applicable to this
change). Terminal state `dismissed`. Raised by a reply whose actual author has
effective repository write access, or by a 👎-dominant reaction — but a reaction
is **ledger-only** (records the state for suppression memory, never resolves the
thread).

### Deferred

The PR closed **without merging** — the finding was acknowledged but not fixed.
Terminal state `deferred`, written by the gauge on an unmerged close.

### Resolved

A maintainer explicitly closed the open Argus threads by running `@argus resolve`.
Terminal state `resolved`, kept distinct from addressed/dismissed so it never
poisons the gauge's rates. Maintainer-only: the command checks
`author_association ∈ {owner, member, collaborator}` before acting.

---

## A note on GitHub permissions

"Write access" on GitHub means push permission. Argus reads it two ways, and
the two are not equivalent.

- `HasRepoWriteAccess` (`internal/github/client.go`) asks GitHub's permission
  API about one login. It accepts `admin`, `maintain`, and `write`. This is the
  exact answer and is required for every reply-derived persistent write.
- `IsPrivilegedAssociation` (`internal/github/identity.go`) reads the
  `author_association` field on a webhook payload. It accepts `OWNER`,
  `MEMBER`, and `COLLABORATOR`. This is only a coarse proxy and cannot authorize
  reply-derived writes.

`CONTRIBUTOR` means only that the person had a pull request merged. It grants
nothing. `FIRST_TIME_CONTRIBUTOR`, `MANNEQUIN`, and `NONE` also grant nothing.

Both checks fail closed. An empty or unknown value denies. A permission lookup
that errors denies.
