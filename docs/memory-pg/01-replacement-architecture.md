# Replacement architecture: pgvector-native memory store

## 1. The engine decision

### Polygres (Evokoa pgContext + pgGraph) — evaluated, rejected for now

What it is: two Apache-2.0 Rust/pgrx extensions. pgContext = "Qdrant-like
retrieval inside Postgres" (collections registered over your own tables, HNSW
ANN with exact MVCC re-scoring, Qdrant-style JSON filters, dense+FTS RRF
hybrid). pgGraph = FK-projected graph traversal (BFS expand, shortest path,
openCypher read subset). The managed cloud + Python SDK are irrelevant to us
(no Go SDK; SQL is the integration surface either way).

Why not now — 2 through 5 are each disqualifying on their own for pgContext (1
has since been retracted; see below). 6 is a separate judgment about pgGraph:
not a disqualifier, just no reason to adopt it.

1. ~~**Postgres version wall.** pgContext supports PG 17/18 only. We run
   `postgres:16` (docker-compose) and Fly Postgres in prod — adoption requires a
   fleet major-version migration *plus* a custom DB image.~~
   **RETRACTED 2026-08-02.** Prod is `argus-db` on PostgreSQL 17.7, already
   served from a custom image (`argus-db:pgvector-17.7-0.8.5`) — inside
   pgContext's 17/18 support, with the custom-image cost already paid. What
   survives is the self-host compose default (`pgvector/pgvector:pg16`), and
   that is not a version wall but disqualifier 2: managed self-hosters cannot
   run the extension on *any* version. This one no longer stands alone.
2. **Self-hoster exclusion.** `superuser=true` + a custom index access method
   means RDS, Aurora, Supabase, Cloud SQL, and Neon can *never* run it until
   vendors allow-list it (none do). pgvector is in every default catalog. For an
   AGPL self-hosted product this is structural, not cosmetic.
3. **Maturity.** pgContext was published 11 days before this research (81
   commits, all in a 4-day burst; one contributor; zero independent users,
   benchmarks, or reviews). The features beyond exact search — HNSW ANN itself,
   sparse, late-interaction, composite fusion, quantization — are all labeled
   **Experimental** by the project's own maturity contract, and the HNSW on-disk
   format has no compatibility promise across upgrades (REINDEX risk).
4. **Score-contract break.** Its hybrid `pgcontext.query` returns an RRF-fused
   score that is *not* a similarity. Dismissal suppression (`>= 0.85` drop)
   requires absolute [0,1] similarities; hybrid results would silently break the
   thresholds. (Dense-only search does return derivable similarity — but then
   we are not using the thing that differentiates it.)
5. **In-database blast radius.** A Rust extension bug inside the production
   Postgres process is a database incident. Today a Supermemory outage degrades
   behind BestEffort; this trade would move memory failures into the primary
   datastore.
6. **pgGraph is YAGNI.** Our memory is flat documents + metadata; no current
   read path needs FK-graph traversal. Its trigger-sync + mmap engine + pg_cron
   maintenance is ops weight for zero current feature. Relational wins we do
   want (03-improvements.md) are plain SQL joins.

**Update 2026-08-02 — evaluated in depth and DECLINED. The hedge below was
wrong on its central claim; it is kept only as a record of what we believed.**

~~The hedge that keeps the door open: one `memories` table with a vector
column, jsonb metadata, and a `container_tag` column is structurally identical
to a pgContext collection registration (`create_collection`,
`register_vector`, `register_filter_column`). If pgContext matures
(PG-version reach, managed-PG allow-listing, Stable ANN), it can be layered on
top of this schema without a data migration.~~

**Why that was wrong.** `resolve_vector_column` (`catalog.rs:437-448`) gates
registration on `atttypid = 'pgcontext.vector'::regtype`. Our column is
`public.vector(1024)` (migration 059). Adopting the collection API therefore
requires retyping the column — a data migration, and one that sits *below* the
`memory.Indexer` seam in `store/postgres.go`'s app-wide `AfterConnect`, whose
own comment notes that a failure there fails every connection. The schema is
not "structurally identical"; it is one type-identity check away from
incompatible.

**Four findings, any one of which is disqualifying** (executed against the
source, 2026-08-02):

1. `pgcontext.query()` — the only fused dense+FTS entry point — takes **no
   filter parameter**. No `installation_id`, no `container_tag`, no tombstone
   predicate on the hybrid path. On a multi-tenant deployment that is a
   cross-tenant read, not a performance trade.
2. Its fused RRF **discards cosine** (the value caps at 2/61 ≈ 0.033), so our
   absolute floors match nothing and dismissal suppression **fails
   silent-open** — verbatim risk #1 in `02-migration-plan.md`.
3. The filtered-candidate mask has a **compile-time 10,000-row ceiling** with
   silent physical-prefix truncation: the same silent-recall-dip class
   migration 060's partial-index predicate exists to prevent.
4. It needs superuser `CREATE EXTENSION` plus a custom index access method,
   which excludes Neon/RDS/Supabase — the managed providers this document and
   `README.md` name as supported self-host targets.

**And the upside was not there.** Our score is recomputed from the live heap
row *after* fusion (`pgsearch.go`: `FROM fused u JOIN memories m ON m.id =
u.id`); the ANN index supplies rank only. So swapping engines provably cannot
change any suppression, attribution, or dedupe decision — it is a latency
experiment, on reads that are already sub-millisecond inside a 30-120s LLM
pipeline. The only capability we lack and could not cheaply build is ColBERT
late-interaction reranking, and it is unreachable anyway (no multi-vector
provider in `embed_catalog.go`; MaxSim has no calibration path to an absolute
[0,1] floor). Sparse vectors are ~8 lines on pgvector 0.8.5's existing
`sparsevec`; grouped search is a `PARTITION BY` in our own CTE.

Fair credit: pgContext's own benchmark docs are unusually honest — they
volunteer the 5-100x filtered-latency losses and a 42.9s compaction stall —
and its supply-chain hygiene is strong. Worth re-checking in a year; not worth
a pilot now. Same superuser/managed-PG objection applies to pgGraph.

### pgvector + core FTS — selected

- Available everywhere we and our self-hosters run Postgres. Fly Managed
  Postgres lists it in the extensions catalog; unmanaged Fly Postgres needs a
  pgvector-enabled image (one-time infra task); docker-compose changes one line
  (`postgres:16` → `pgvector/pgvector:pg16`); RDS/Aurora/Supabase/Neon all ship it.
- Run **pgvector >= 0.8.2** (0.8.0 added iterative index scans — exactly what
  our heavily-filtered ANN queries need; 0.8.2 fixes a parallel-HNSW-build
  overflow, CVE-2026-3172).
- At our scale (single-digit-thousands of docs per install, low QPS), exact vs
  ANN performance is a non-issue; HNSW is chosen over IVFFlat because tenants
  grow from near-empty (IVFFlat needs representative data at build time).
- Extension footprint: pgvector ONLY. FTS is core Postgres (`tsvector`,
  `ts_rank_cd`). No pg_trgm/unaccent until a concrete need appears.

### Postgres version policy (decided 2026-08-01, spike-verified)

Two-track "try for 19":

- **Dev + CI run PostgreSQL 19beta2** with pgvector built from master (upstream
  closed "PG 19 support" 2026-07-29; spike verified: builds clean, extversion
  0.8.6, full design-doc SQL passes — schema, partial HNSW, upsert-REPLACE,
  `hnsw.iterative_scan=relaxed_order`, ScopeBoth + metadata filters + cosine
  score contract, hybrid RRF CTE). Every program PR is proven against 19 from
  day one.
- **Prod is ALREADY capable (2026-08-02):** Fly app `argus-db`, PostgreSQL
  17.7 + pgvector 0.8.5 via custom image `registry.fly.io/argus-db:pgvector-17.7-0.8.5`
  — and note well: **the application data lives in the `postgres` database**
  (not an app-named one). No major upgrade is required by this program; a
  deliberate 18/19 bump is optional later (an `argus-db-18` cluster + image
  exist if wanted).
- **Self-host compose default is `pgvector/pgvector:pg16` today** (same major
  as before — existing `db-data` volumes keep working; extension ready for
  migration 057). The major bump to 18/19 ships with the prod-cutover step,
  with documented dump/restore upgrade notes. Managed-PG self-hosters
  (RDS/Supabase/Neon) already have pgvector on their current version.
- (Historical) the PG-19 note about the pgContext hedge is moot: pgContext was
  evaluated and declined on 2026-08-02 — see the section above.

## 2. Schema (cumulative: migrations 057, 059, 060)

Current shape, not any single migration. 057 created the table, 059 retyped the
embedding to 1024 dims, 060 added the invalidation tombstone and rebuilt both
partial indexes around it. Per-migration attribution is called out inline below.

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE memories (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  installation_id bigint NOT NULL REFERENCES installations(id),  -- tenant; FK so a writer bug filing memories under a bogus id fails loudly instead of leaking scope
  container_tag   text   NOT NULL,                -- RepoTagNew(repo) or '_shared' (builders reused verbatim)
  custom_id       text   NOT NULL,                -- existing deterministic IDs, unchanged
  type            text   NOT NULL,                -- pattern|scenario|feedback|synthesis|pr_summary|review|topology|rule
  content         text   NOT NULL,
  metadata        jsonb  NOT NULL DEFAULT '{}',   -- the same flat string map Metadata.ToMap emits
  embedding       vector(1024),                   -- 057 shipped vector(1536); 059 retyped to 1024. NULL = embed pending (fail-open write path)
  embedding_model text,
  content_tsv     tsvector GENERATED ALWAYS AS (to_tsvector('english', left(content, 8000))) STORED,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  deleted_at      timestamptz,
  invalidated_at  timestamptz,                    -- 060: memory that became WRONG, excluded like deleted_at
  superseded_by   bigint REFERENCES memories(id), -- 060: links the replacement, history preserved
  CONSTRAINT memories_custom_uniq UNIQUE (installation_id, custom_id)
);

-- Both partial indexes carry the 060 predicate: an ANN index still holding
-- invalidated rows returns neighbours that post-filter away — a silent recall
-- dip on the suppression path.
CREATE INDEX memories_embedding_hnsw ON memories USING hnsw (embedding vector_cosine_ops)
  WHERE deleted_at IS NULL AND invalidated_at IS NULL AND embedding IS NOT NULL;
CREATE INDEX memories_scope_idx ON memories (installation_id, container_tag, type)
  WHERE deleted_at IS NULL AND invalidated_at IS NULL;
CREATE INDEX memories_tsv_gin  ON memories USING gin (content_tsv);
CREATE INDEX memories_meta_gin ON memories USING gin (metadata jsonb_path_ops);
```

Upsert **replaces** (deterministic REPLACE, killing Supermemory's batch
content-merge-on-collision quirk and the `--new-shape-since` defense built
around it):

```sql
INSERT INTO memories (...) VALUES (...)
ON CONFLICT (installation_id, custom_id) DO UPDATE
SET type=EXCLUDED.type, content=EXCLUDED.content, metadata=EXCLUDED.metadata,
    embedding=EXCLUDED.embedding, embedding_model=EXCLUDED.embedding_model,
    container_tag=EXCLUDED.container_tag, updated_at=now(), deleted_at=NULL;
```

`invalidated_at`/`superseded_by` are deliberately absent from the SET list:
invalidation is a policy judgment ("this knowledge is wrong") that a mechanical
re-write of the same content must not silently overturn. `deleted_at` does
reset — a re-index of the same customId is a deliberate recreate.

`UNIQUE (installation_id, custom_id)` mirrors Supermemory's account-scoped
customId exactly (the BYOK key *was* the tenant); container prefixes inside the
IDs already prevent cross-container collisions.

**Deleted by design:** the 600-doc batch cap, per-install rate limiter
(`ratelimit.go`), 429 backoff choreography for a third-party quota, the
`DisableLLMFilter` account mutation, and the nightly `_shared` decay
metadata-rewrite (becomes a query-time expression over `updated_at` — the
reconcile cron's decay phase and its manual-image-update gotcha go away).

## 3. Search: score contract first

The seam consumes `Score` as an **absolute [0,1] similarity** compared against
fixed floors (0.85 drop / 0.60 downgrade / 0.50 enrich...). Therefore:

- RRF (vector rank + FTS rank) plus a recency term **reorders candidates only**.
- The reported `Score` is always `1 - (embedding <=> qvec)` — cosine similarity.
- `MemoryQuery.Threshold` filters on that similarity, never the fused rank.
- A hit found only lexically with sub-threshold similarity is excluded when a
  threshold is set — matching today's semantics.

One SQL per leg, compiled from `MemoryQuery` by a small builder. `ScopeBoth`
becomes `container_tag = ANY($tags)` in ONE query (the `searchFanOut` goroutine
merge in reader.go:120-148 is deleted). Equality filters compile to
`metadata @> $json`; `FilterNumeric` compiles to a cast predicate; per-query
`SET LOCAL hnsw.ef_search = 40; SET LOCAL hnsw.iterative_scan = relaxed_order;`.

Leg notes:
- **Dismissal search** is vector-dominant; its Score is the raw cosine
  similarity suppression thresholds against — comparable by construction.
- **Briefing synthesis leg** stops being a fake vector search (placeholder
  query + metadata AND-filter) and becomes an honest indexed point lookup:
  `WHERE type='synthesis' AND metadata->>'file_path' = $1 LIMIT 1`.
- **Rerank flag**: Supermemory's server-side rerank is dropped; RRF+recency is
  the rerank. The field stays in `MemoryQuery`, ignored — no caller churn.
- **Enrich/RichContent** (related-memories summaries) has no equivalent;
  hint paths degrade to content-only. Acceptable; revisit only if hint quality
  measurably drops.

## 4. Embedding pipeline

- **Default model: `voyage-4`** (SOTA-validated 2026-08-02: RTEB 70.1 vs 55.21
  for text-embedding-3-small on short English prose; native 1024 dims = the
  storage dimensionality). OpenAI `text-embedding-3-*` BYOK models
  Matryoshka-truncate to 1024 via their `dimensions` param; custom endpoints
  must serve 1024-dim models (write path validates). Cost identical order:
  a heavy review ≈ $0.001; full re-embed of the corpus ≈ dollars.
- **BYOK alignment:** resolve keys via the existing
  `store.ResolveAPIKey(installation, repo, provider)` chain (repo → org → env),
  with `base_url` override — so self-hosters point at Ollama/TEI with one env
  var. The Supermemory `Registry` (key decrypt, client cache, singleflight,
  filter-disable) collapses into a thin `EmbedderRegistry`. `SUPERMEMORY_API_KEY`
  dies.
- **Interface:** `internal/memory/embed.go` —
  `Embed(ctx, inputs []string) ([][]float32, error)`; raw net/http mirroring
  `supermemory.go`'s doRequest, reusing the existing `backoff.go` retry policy.
  No SDK dependency.
- **Index path:** embed synchronously before upsert; batch writes pass all
  bodies in one embeddings request. **On embed failure: INSERT with
  `embedding=NULL`, Warn** — the doc is immediately FTS/metadata-searchable; a
  backfill sweep (`WHERE embedding IS NULL LIMIT 200`) retries later. Mirrors
  today's "indexing failures are non-fatal" rule; nothing lost, nothing stalls.
- **Query path:** embed inside the existing 5s read window; an embed timeout
  surfaces as the search error BestEffort degrades — byte-for-byte today's
  resilience contract. Small per-process LRU (sha256(query+model) → vector)
  kills 30-50% of query-embed calls (briefing legs reuse query texts).
- **`embedding_model` column** pins the space; search WHERE clauses filter on
  the current model; model changes ship with a trivial `cmd/reembed-memory`.

## 5. What a replacement must implement (the seam, verbatim)

From `indexer.go:47-83` — 6 writers (IndexReviewCommentsBatch, IndexRule,
IndexPattern, IndexSharedPattern, IndexFeedbackSignal, IndexScenario), Search,
Briefing, DeleteDocument (accepts BOTH id kinds in circulation today: rule
deletes pass deterministic customIds, pattern deletes pass stored server ids —
unify on id==customId), DisableLLMFilter (no-op). All customId builders,
`Metadata.ToMap` validation, briefing dispatch/render, thresholds, suppression
policy, write floor: **unchanged above the seam**.

## 6. Effort

~1,600-2,200 LOC Go + ~90 LOC SQL new; ~2,000 LOC deleted after cutover
(supermemory.go client, registry machinery, ratelimit, SM-specific backoff use,
reconcile decay phase, key-management API + web card). ~6 gated PRs:
schema → embedder → writers → search+briefing → registry+flag+import →
cutover+delete. Calendar: ~1-2 weeks focused work + a 1-2 week shadow-read
window (wall-clock, not effort) for threshold recalibration.

Test dependency: PG-backed round-trip tests need Postgres in CI — the same
blocker already open as issue #138 for sqlc; this project forces it.
