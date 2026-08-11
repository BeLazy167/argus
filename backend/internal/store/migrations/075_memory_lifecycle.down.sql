DROP INDEX IF EXISTS memories_scope_idx;
CREATE INDEX memories_scope_idx ON memories (installation_id, container_tag, type)
  WHERE deleted_at IS NULL AND invalidated_at IS NULL;

DROP TABLE IF EXISTS memory_mirror_outbox;
DROP TABLE IF EXISTS memory_review_attributions;
DROP VIEW IF EXISTS live_memories;
