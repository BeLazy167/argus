-- Temporal invalidation (docs/memory-pg/03-improvements.md §2b): memory that
-- becomes WRONG — a dismissal later confirmed as a real bug, a rule reversed,
-- a pattern whose outcomes collapse — is invalidated, not merely decayed.
-- Search predicates exclude invalidated rows exactly like deleted_at; history
-- is preserved (never deleted), and superseded_by links the replacement.
ALTER TABLE memories ADD COLUMN invalidated_at timestamptz;
ALTER TABLE memories ADD COLUMN superseded_by bigint REFERENCES memories(id);

-- Live-row lookups filter on all tombstones; extend BOTH partial indexes.
-- The HNSW predicate especially: the PR-4 vector leg filters
-- invalidated_at IS NULL, and an ANN index still containing invalidated rows
-- would return neighbours that post-filter away — a silent recall dip on the
-- suppression path. Pre-data, so rebuilds are instant.
DROP INDEX IF EXISTS memories_scope_idx;
CREATE INDEX memories_scope_idx ON memories (installation_id, container_tag, type)
  WHERE deleted_at IS NULL AND invalidated_at IS NULL;
DROP INDEX IF EXISTS memories_embedding_hnsw;
CREATE INDEX memories_embedding_hnsw ON memories
  USING hnsw (embedding vector_cosine_ops)
  WHERE deleted_at IS NULL AND invalidated_at IS NULL AND embedding IS NOT NULL;
