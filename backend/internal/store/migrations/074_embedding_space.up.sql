-- Identify the full embedding coordinate system. Existing rows are left NULL
-- intentionally: the re-embed sweep treats NULL as a foreign/unknown space and
-- repairs them with the active endpoint/model/dimensionality.
ALTER TABLE memories ADD COLUMN embedding_space text;

CREATE INDEX memories_reembed_space_idx
  ON memories (installation_id, embedding_space, id)
  WHERE deleted_at IS NULL AND invalidated_at IS NULL;
