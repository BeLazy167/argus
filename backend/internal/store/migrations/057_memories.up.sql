-- Memory-in-Postgres (docs/memory-pg/01 §2): the memories store that replaces
-- Supermemory behind the memory.Indexer seam. Inert until the pgIndexer lands;
-- nothing reads or writes this table yet.
--
-- Score contract: retrieval reports 1 - (embedding <=> query) cosine
-- similarity in [0,1]; hybrid fusion may reorder but never replaces the score.
CREATE EXTENSION IF NOT EXISTS vector;

-- Fail fast on pgvector versions missing iterative index scans (0.8.0) and the
-- parallel-HNSW-build fix (0.8.2, CVE-2026-3172). Without this, an old
-- managed-PG extension would pass migration and then break memory search at
-- query time on SET hnsw.iterative_scan.
DO $$
DECLARE v text;
BEGIN
  SELECT extversion INTO v FROM pg_extension WHERE extname = 'vector';
  IF string_to_array(v, '.')::int[] < ARRAY[0, 8, 2] THEN
    RAISE EXCEPTION 'pgvector >= 0.8.2 required, found %', v;
  END IF;
END $$;

CREATE TABLE memories (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  -- Tenant. Supermemory's tenancy was implicit ("BYOK key = account"); here it
  -- is an explicit column, and (installation_id, custom_id) mirrors the
  -- account-scoped customId uniqueness the deterministic ID builders assume.
  -- FK matches every peer tenant table: a writer bug inserting a bogus or
  -- GitHub-numbered installation id must fail loudly here, not file memories
  -- under (or leak them into) another tenant's scope. Installations are only
  -- ever suspended, never deleted, so no CASCADE is needed.
  installation_id bigint NOT NULL REFERENCES installations(id),
  -- RepoTagNew(repo) or '_shared' — the existing tag builders are reused
  -- verbatim, reinterpreted as a column instead of a remote container.
  container_tag   text   NOT NULL,
  custom_id       text   NOT NULL,
  type            text   NOT NULL,
  content         text   NOT NULL,
  -- The flat string map Metadata.ToMap emits (schema_version=1).
  metadata        jsonb  NOT NULL DEFAULT '{}',
  -- NULL = embedding pending (fail-open write path: rows land FTS-searchable
  -- immediately; a backfill sweep retries embedding later).
  embedding       vector(1536),
  embedding_model text,
  content_tsv     tsvector GENERATED ALWAYS AS (to_tsvector('english', left(content, 8000))) STORED,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  deleted_at      timestamptz,
  CONSTRAINT memories_custom_uniq UNIQUE (installation_id, custom_id)
);

-- HNSW over live embedded rows only. Defaults (m=16, ef_construction=64) are
-- ample at current scale; per-query tuning happens via SET LOCAL
-- hnsw.ef_search / hnsw.iterative_scan.
CREATE INDEX memories_embedding_hnsw ON memories
  USING hnsw (embedding vector_cosine_ops)
  WHERE deleted_at IS NULL AND embedding IS NOT NULL;

CREATE INDEX memories_scope_idx ON memories (installation_id, container_tag, type)
  WHERE deleted_at IS NULL;

CREATE INDEX memories_tsv_gin ON memories USING gin (content_tsv);

CREATE INDEX memories_meta_gin ON memories USING gin (metadata jsonb_path_ops);
