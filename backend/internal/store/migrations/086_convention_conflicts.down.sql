CREATE OR REPLACE VIEW live_memories AS
SELECT * FROM memories
WHERE deleted_at IS NULL AND invalidated_at IS NULL AND superseded_by IS NULL;
DROP TABLE IF EXISTS convention_conflicts;
DROP TABLE IF EXISTS convention_evidence;
DROP INDEX IF EXISTS patterns_convention_memory_custom_uniq;
