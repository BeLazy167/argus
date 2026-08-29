DROP INDEX IF EXISTS memories_embedding_hnsw;
CREATE INDEX memories_embedding_hnsw ON memories
  USING hnsw (embedding vector_cosine_ops)
  WHERE deleted_at IS NULL AND embedding IS NOT NULL;
DROP INDEX IF EXISTS memories_scope_idx;
CREATE INDEX memories_scope_idx ON memories (installation_id, container_tag, type)
  WHERE deleted_at IS NULL;
ALTER TABLE memories DROP COLUMN IF EXISTS superseded_by;
ALTER TABLE memories DROP COLUMN IF EXISTS invalidated_at;
