# Memory-module improvements unlocked by the move

## 1. Current weaknesses (with evidence)

**Latent bugs found during this research** (worth fixing regardless of engine):

1. **Dismissal recurrence is undercounted by upsert.** `dismissalCustomID` is
   keyed by category+content only, so the identical dismissal recurring across
   PRs upserts ONE doc (indexer.go:259-270) — yet the team-feedback drop rule
   requires `SuppressSimilarCount=3` DISTINCT similar docs (suppression.go:53-58).
   A team dismissing the identical finding 5× counts as 1; three reworded
   dismissals count as 3. The intended "recurrence" signal is never stored.
2. **Scenario dedupe degrades into duplicate creation.** Under a memory outage
   the dedupe check errors and the code proceeds to create the seed
   (scenarios.go:116-127) — an outage mints duplicate scenario rows.
3. **Novelty is persisted as false when search broke.** The enricher correctly
   distinguishes error-from-empty in memory (`IsNewFinding` unset on error), but
   the pipeline persists it as `false` to review_comments (orchestrator.go:4487)
   — the DB can't distinguish "not novel" from "search failed."

**Structural weaknesses:**

- **The 5s timeout papers over failure.** Every read owns a private 5s ctx and
  BestEffort degrades to the zero value with a Warn. No read-level retry; a
  timeout is terminal for that read. When it trips a review silently loses:
  briefing (incl. the DO-NOT-REFLAG list), triage hints, scoring calibration,
  and — worst — dismissal suppression (the exact FP the team dismissed
  re-posts) while the pipeline reports success.
- **Two-store identity dance:** search hits may carry chunk ids, so custom_id is
  mirrored into metadata, resolved via `GetPatternIDByCustomID` with a
  `supermemory_id` fallback, with mirror columns (migs 006/027/045/052) and two
  reconciliation CLIs keeping the stores agreeing.
- **~120 external searches per 40-file deep review** (ratelimit.go:11-14), each
  an HTTPS round trip behind a token bucket, retry/backoff, singleflighted
  client registry — all of which exists only because the store is remote.
- **Retrieval is unobservable:** `SearchResponse.Timing` is decoded and never
  read; hit counts log at Debug; the assembled briefing is never persisted —
  retrieved-vs-used analysis and threshold tuning are impossible today.
- **Global one-size thresholds:** a single 0.50 floor gates six different read
  kinds; nothing is calibrated per memory type or finding category.
- **Briefing staleness:** file history relies on synthesis docs written
  post-review; `_shared` decay is a nightly external cron; the repo container
  has no recency weighting at all.

## 2. Prioritized improvements

| # | Improvement | Impact | Effort | Notes |
|---|---|---|---|---|
| 1 | **Patterns + dismissals in Postgres with PK-returning vector search** | Very high | Medium | Search returns the pattern row PK directly — deletes resolvePatternID's identity dance and the mirror columns; pattern_stats bumps stop missing on chunk-id mismatches, sharpening judge Pattern Trust calibration. Suppression stops turning off during outages. |
| 2 | **Lifecycle-joined dismissal suppression** | High | Small (after #1) | Dismissed findings already exist as review_comments rows: COUNT(DISTINCT pr_number) fixes the recurrence undercount; require category agreement or module-cluster adjacency (graph tables are already in PG) for the ≥0.85 drop; weight who dismissed; veto suppression when the class was later CONFIRMED. Attacks both suppression error modes. |
| 3 | **SQL-assembled briefing with live file history** | High | Medium | One CTE replaces 5-7 remote searches per file: live findings+outcomes on this path (no synthesis-doc staleness), top-k hybrid patterns, DO-NOT-REFLAG from actual dismissed rows, rules, repeat-offender stats ("this file: 4 nil-deref findings in 90 days"). Read-your-writes fixes the queued-indexing blind spot for fast follow-up pushes (incremental re-review). Render layer unchanged. |
| 4 | **Retrieval observability** | Medium-high | Small | Persist per-review retrieved doc ids + scores + rendered sections; export latency. Precondition for tuning thresholds/decay/dedup and for proving memory ROI. |
| 5 | **Query-time recency decay** | Medium | Small | `similarity * exp(-λ·age)` in ranking replaces the nightly `_shared` metadata-rewrite cron (and its manual-image gotcha); decay becomes continuous; retirement becomes a WHERE clause. |
| 6 | **Hybrid FTS+vector with honest predicates** | Medium | Medium | Queries embed literal file paths/categories; FTS leg catches them lexically. The briefing synthesis leg becomes an indexed point lookup instead of a placeholder-query metadata search. |
| 7 | **Atomic write path** | Medium | Small-medium | Memory writes transactional with review persistence; ON CONFLICT replaces batch collision-merge; no queued-processing lag; kills the split-brain class. |
| 8 | **Per-class thresholds fed by pattern_stats precision** | Medium | Small | Floors keyed (memory type, finding category); settings plumbing exists, only keying is missing. Sequence after #4 so tuning is evidence-based. |

## 3. Sequencing note

#1 is the migration itself (01/02 docs). #2, #5, #7 are near-free riders on the
new store and should land in the same program. #3 and #4 are fast-follows that
convert the move from "vendor removal" into a **review-quality upgrade** — the
briefing gets fresher and richer, and suppression gets precise. #6 partially
ships inside #1 (the hybrid SQL); #8 waits for #4's data.
