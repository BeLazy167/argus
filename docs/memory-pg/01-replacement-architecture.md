# Replacement architecture: pgvector-native memory store

## 1. The engine decision

### Polygres (Evokoa pgContext + pgGraph) — evaluated, rejected for now

What it is: two Apache-2.0 Rust/pgrx extensions. pgContext = "Qdrant-like
retrieval inside Postgres" (collections registered over your own tables, HNSW
ANN with exact MVCC re-scoring, Qdrant-style JSON filters, dense+FTS RRF
hybrid). pgGraph = FK-projected graph traversal (BFS expand, shortest path,
openCypher read subset). The managed cloud + Python SDK are irrelevant to us
(no Go SDK; SQL is the integration surface either way).

Why not now — each of these is disqualifying on its own:

1. **Postgres version wall.** pgContext supports PG 17/18 only. We run
   `postgres:16` (docker-compose) and Fly Postgres in prod — adoption requires a
   fleet major-version migration *plus* a custom DB image.
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

**The hedge that keeps the door open:** one `memories` table with a vector
column, jsonb metadata, and a `container_tag` column is structurally identical
to a pgContext collection registration (`create_collection` + `register_vector`
+ `register_filter_column`). If pgContext matures (PG-version reach, managed-PG
allow-listing, Stable ANN), it can be layered on top of this schema without a
data migration. Re-evaluate in 6-12 months.

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
- **Prod cutover targets 19-GA-if-released, else 18** (GA expected Sept/Oct
  2026). Betas never touch prod: no supported beta→GA upgrade path, and
  pgvector-on-19 is unreleased master code. Fallback is a custom
  `flyio/postgres-flex:18` + pgvector 0.8.6 image; the 18→19 bump post-GA is
  its own small step.
- **Self-host compose default is `pgvector/pgvector:pg16` today** (same major
  as before — existing `db-data` volumes keep working; extension ready for
  migration 057). The major bump to 18/19 ships with the prod-cutover step,
  with documented dump/restore upgrade notes. Managed-PG self-hosters
  (RDS/Supabase/Neon) already have pgvector on their current version.
- Known trade: on PG 19 the pgContext hedge stays closed until it adds 19
  support (17/18-only today).

## 2. Schema (migration 057)

```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE memories (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  installation_id bigint NOT NULL,                -- tenant (today implicit in "BYOK key = tenant")
  container_tag   text   NOT NULL,                -- RepoTagNew(repo) or '_shared' (builders reused verbatim)
  custom_id       text   NOT NULL,                -- existing deterministic IDs, unchanged
  type            text   NOT NULL,                -- pattern|scenario|feedback|synthesis|pr_summary|review|topology|rule
  content         text   NOT NULL,
  metadata        jsonb  NOT NULL DEFAULT '{}',   -- the same flat string map Metadata.ToMap emits
  embedding       vector(1536),                   -- NULL = embed pending (fail-open write path)
  embedding_model text,
  content_tsv     tsvector GENERATED ALWAYS AS (to_tsvector('english', left(content, 8000))) STORED,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  deleted_at      timestamptz,
  CONSTRAINT memories_custom_uniq UNIQUE (installation_id, custom_id)
);

CREATE INDEX memories_embedding_hnsw ON memories USING hnsw (embedding vector_cosine_ops)
  WHERE deleted_at IS NULL AND embedding IS NOT NULL;
CREATE INDEX memories_scope_idx ON memories (installation_id, container_tag, type) WHERE deleted_at IS NULL;
CREATE INDEX memories_tsv_gin  ON memories USING gin (content_tsv);
CREATE INDEX memories_meta_gin ON memories USING gin (metadata jsonb_path_ops);
```

Upsert **replaces** (deterministic REPLACE, killing Supermemory's batch
content-merge-on-collision quirk and the `--new-shape-since` defense built
around it):

```sql
INSERT INTO memories (...) VALUES (...)
ON CONFLICT (installation_id, custom_id) DO UPDATE
SET content=EXCLUDED.content, metadata=EXCLUDED.metadata, embedding=EXCLUDED.embedding,
    container_tag=EXCLUDED.container_tag, updated_at=now(), deleted_at=NULL;
```

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

- **Default model: OpenAI `text-embedding-3-small`** (1536 dims, $0.02/1M tok,
  fits pgvector's 2000-dim index cap). Cost reality: a heavy review writes
  ~20-60 docs ≈ $0.0004; full 1M-doc re-embed ≈ $6. Cost is a non-argument.
  Voyage (`voyage-3.5-lite`) is a same-price alternative; code-tuned models buy
  little because memory content is deliberately prose (diffs are stripped).
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
