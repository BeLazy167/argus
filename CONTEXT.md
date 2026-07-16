# Argus Domain Glossary

Shared vocabulary for the re-review and comment-lifecycle behavior. Each term is
grounded in the code that implements it; see [docs/architecture.md](docs/architecture.md)
for the full flow.

### Finding

One issue Argus raises, anchored to a `file:line` and posted as a GitHub review
comment. Persisted in `review_comments`; carries exactly one lifecycle **state**
(see below). The unit everything else in this glossary acts on.

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
change). Terminal state `dismissed`. Raised by a privileged reply, or by a
👎-dominant reaction — but a reaction is **ledger-only** (records the state for
suppression memory, never resolves the thread).

### Deferred

The PR closed **without merging** — the finding was acknowledged but not fixed.
Terminal state `deferred`, written by the gauge on an unmerged close.

### Resolved

A maintainer explicitly closed the open Argus threads by running `@argus resolve`.
Terminal state `resolved`, kept distinct from addressed/dismissed so it never
poisons the gauge's rates. Maintainer-only: the command checks
`author_association ∈ {owner, member, collaborator}` before acting.

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
