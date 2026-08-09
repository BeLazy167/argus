-- Reverses 062. The pattern_stats UNIQUE goes back to its global form, which
-- is why this direction is unsafe to run once any Postgres-written customId
-- exists: two tenants sharing a repo name can hold the same memory_doc_id, and
-- restoring the global constraint would fail outright (better than merging).
ALTER TABLE decision_traces ADD COLUMN IF NOT EXISTS supermemory_id TEXT;
CREATE INDEX IF NOT EXISTS idx_decision_traces_pending_sm
    ON decision_traces(repo_id)
    WHERE supermemory_id IS NULL;

ALTER TABLE pattern_stats DROP CONSTRAINT IF EXISTS pattern_stats_install_doc_key;
ALTER TABLE pattern_stats RENAME COLUMN memory_doc_id TO supermemory_id;
ALTER TABLE pattern_stats ADD CONSTRAINT pattern_stats_supermemory_id_key UNIQUE (supermemory_id);

ALTER INDEX IF EXISTS idx_scenarios_pending_index RENAME TO idx_scenarios_pending_sm;
ALTER TABLE scenarios RENAME COLUMN memory_doc_id TO supermemory_id;

ALTER INDEX IF EXISTS idx_patterns_memory_custom_id RENAME TO idx_patterns_supermemory_custom_id;
ALTER TABLE patterns RENAME COLUMN memory_custom_id TO supermemory_custom_id;
ALTER TABLE patterns RENAME COLUMN memory_doc_id TO supermemory_id;
