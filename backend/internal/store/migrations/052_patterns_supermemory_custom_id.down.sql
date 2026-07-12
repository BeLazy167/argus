DROP INDEX IF EXISTS idx_patterns_supermemory_custom_id;
ALTER TABLE patterns DROP COLUMN IF EXISTS supermemory_custom_id;
