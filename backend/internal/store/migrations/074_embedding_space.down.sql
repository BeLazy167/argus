DROP INDEX IF EXISTS memories_reembed_space_idx;
ALTER TABLE memories DROP COLUMN IF EXISTS embedding_space;
