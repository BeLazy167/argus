# Migration plan: moving memories out of Supermemory

## 1. What lives in Supermemory (class inventory)

| Class (type / source) | customId scheme | Postgres source of truth | Re-derivable? |
|---|---|---|---|
| review (posted findings passing write floor) | `{repo}--{file}--h(file\|cat\|normBody)` | review_comments ⋈ reviews/repos | ~Yes — PG stores the GitHub-formatted body, not the raw body that was hashed, so re-derived customIds differ from live docs. Irrelevant once the new store keys by `review_comments.id` FK. |
| pattern / scoring_confirmed | `{repo}--confirmed--h` | patterns (supermemory_custom_id col, mig 052) | Byte-exact |
| pattern / auto_learn (repo + org-promoted _shared) | `{repo}--learned--h`, `--org_learned--h` (empty leading segment — `PatternCustomID("", "org_learned", ...)`, pinned by pipeline_customid_test.go; NOT `shared--...`, which is SharedPatternCustomID's shape and never fires on this path) | patterns (repo_id NULL = shared) | Byte-exact |
| pattern / convention | `{repo}--convention--h(RAW convention)` | patterns | Byte-exact (hash the raw text, not the wrapped content — `pipeline_customid.go:42-70`) |
| synthesis (per-file LLM file memory) | `{repo}--{path}--h--synthesis` | **NONE — SM-only** | No. Inputs exist in review_comments; re-synthesis possible at LLM cost, or organic rebuild. |
| pr_summary | `{repo}--pr-N-summary` | reviews columns | Approximate (files list approximated) |
| topology / arch_summary | `arch-summary:{owner}--{repo}` | code_graph (GetTopChokePoints) | Regenerates deterministically on next review — no backfill needed |
| feedback / confirmed (praise, replies, reactions) | `{repo}--feedback--h` | comment_outcomes(confirmed) ⋈ review_comments | Mostly (developer-reply suffixes in content are SM-only) |
| feedback / dismissed (the suppression corpus) | `{repo}--dismissal--h(cat\|normBody)` — deliberately file-path-free | comment_outcomes(dismissed) | Body yes; **`reason` extras (LLM-distilled, 300c) are SM-only** |
| feedback / ignored | `{repo}--feedback--h` | comment_outcomes(ignored) | Yes (prior backfill skipped this class; include it now) |
| scenario | `{repo}--scenario--{id}` | scenarios table | Byte-exact |
| rule | `rule--{id}` (_shared) | rules table | Byte-exact |
| pattern / reply_feedback (_shared "Learnings") | `shared--reply_feedback--h` | **NONE — SM-only** | No. |
| trace | — | decision_traces | Retired; nothing to migrate. |

Non-doc state in SM: (a) `_shared` decayed confidence values (live only in SM
metadata — re-derive resets to 1.00); (b) embeddings/chunks — **never
exportable under any strategy**, so re-embedding + threshold re-tune is
unavoidable regardless. This removes the main argument for export-as-primary.

**Verdict: ~90% of the corpus re-derives cleanly from Postgres.**

## 2. Strategy: hybrid, re-derive-first

1. **Primary — re-derive** every PG-backed class via a new `cmd` cloned from
   `cmd/migrate-memory` (reuse its `migrate.sql` sweep queries, `mappers.go`
   mapping logic, keyset pagination, circuit breaker, `--plan` dry-run,
   `--installation` scoping). Major simplification vs prior art: rows key by
   native FKs + content hash, so the entire customId-reconstruction /
   `--new-shape-since` merge-corruption machinery disappears — it existed only
   because Supermemory's batch API merges on customId collision.
2. **Safety net + SM-only rescue — one-shot export** BEFORE anything else:
   `ListDocuments` per container (`includeContent=true`, limit 200/page; a
   50k-doc install exports in minutes), archived raw into a
   `memory_export_archive` jsonb table. From the archive import ONLY the
   SM-only classes: synthesis docs (metadata carries file_path), reply_feedback
   learnings, optionally `_shared` confidence values. Do NOT import PG-backed
   classes from export — re-derivation skips merge-corrupted docs and legacy
   shapes.
3. Installs with revoked BYOK keys cannot be exported — re-derive-only; their
   SM-only classes are permanently lost (accepted).

## 3. Cutover phases

| Phase | What | Rollback |
|---|---|---|
| 0. Snapshot | Export archive per install; pause reconcile-memory decay (or `--plan` only) so retirement deletions can't race; ship migration repointing `pattern_stats` off `supermemory_id` UNIQUE onto `patterns.supermemory_custom_id`/id (052's column is the bridge) | Nothing changed |
| 1. Build | PG-native Indexer behind the existing seam; per-installation backend selection in `Registry.GetIndexer` via feature_flags (mig 030 — remember: flags are dormant until wired) | Flag off |
| 2. Backfill | Re-derive all PG-backed classes; import SM-only classes from archive; batch-embed during backfill. Idempotent via unique constraints | TRUNCATE new tables |
| 3. Dual-write (1-2 wk) | Tee Indexer writes to SM + PG; SM stays read-authoritative; PG write failures log-only. PG writes are transactional with source rows — the NULL-supermemory_id drift-repair job class dies here | Drop tee |
| 4. Shadow-read + recalibrate | Async duplicate every Search/Briefing against PG; log parity (overlap@k, score pairs) plus the decision-level metrics that matter: dismissal-suppression agreement (drop/downgrade/none per finding), briefing section non-empty rates, scenario-dedupe agreement. Re-tune ALL seven `thresholds.go` floors by quantile-matching the new similarity distribution — match SM's observed suppression *precision*, not its raw threshold. **Gate: ≥95% suppression-decision agreement over ≥1 week** | Shadow is passive |
| 5. Flip reader | Per-installation (dogfood install first); PG authoritative; reverse dual-write (SM failures log-only) so flip-back is instant. Watch Gauge address-rate + dismissal telemetry for briefing regression | Flag flip; SM still current |
| 6. Retire | Stop SM writes; final export snapshot; delete SUPERMEMORY_API_KEY config, `installations.supermemory_key_enc` paths, registry key/filter machinery, supermemory.go, key-management API + web SupermemoryCard; port `_shared` decay to query-time. Optionally bulk-delete containers (BYOK = customer's own account; dormant also acceptable) | Re-add write path from git |

**Cron-machine hazard (every phase):** reconcile-memory runs on a separate Fly
scheduled machine whose image does NOT update on `fly deploy`. Through phases
3-5 it must be paused or dual-targeted, else it mutates SM with stale code
mid-migration. End state: fold decay into the main app or delete outright
(query-time decay removes the need).

## 4. Data-loss / regression risk register

1. **Threshold recalibration is the one semantically risky step.** Suppression
   fails *silent-open* (stops suppressing → dismissed FPs re-post → trust
   damage) if 0.85 is unreachable in the new space; too-aggressive re-tune
   mutes real findings. The thresholds seam was built for exactly this
   (`thresholds.go:6-8`); the work is the empirical shadow pass. Security
   never-suppress exemption must be re-verified in the new impl.
2. SM-only classes lost without Phase-0 export: synthesis file memories,
   reply_feedback learnings, dismissal `reason` extras, reply suffixes.
3. `pattern_stats.supermemory_id` UNIQUE (mig 027): repoint before dropping SM
   ids or live quality counters orphan (they feed judge Pattern Trust).
4. `_shared` decay reset: re-derive pins confidence=1.00, resurrecting
   near-retired patterns for up to grace+decay period — import confidences from
   the archive, or accept a one-time reset.
5. Merge-corrupted docs ('---' content merges): export preserves corruption;
   re-derive silently fixes it. Phase-4 content diffs on these are EXPECTED.
6. Praise-origin positive feedback re-derives only where praise rows persist.
7. Briefing quality: pgvector+FTS ≠ SM hybrid+rerank. Shadow-compare briefing
   sections; watch address-rate after the flip; keep BestEffort degrade wiring.
