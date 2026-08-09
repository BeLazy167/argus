-- Rename the Supermemory-named id columns to backend-neutral names, and fix a
-- cross-tenant UNIQUE that only became reachable once ids stopped coming from
-- Supermemory's server.
--
-- THE UNIQUE IS THE URGENT PART. pattern_stats.supermemory_id was declared
-- `TEXT NOT NULL UNIQUE` — globally unique, with no installation_id. That was
-- safe only while the value was a Supermemory server id, which was unique
-- across the world by construction. PGIndexer instead returns a DETERMINISTIC
-- customId, and memory.PatternCustomID discards its owner argument outright
-- (`_ = owner`), so the id is a function of (repo NAME, source, content) only.
--
-- Two installations that share a repo name and learn the same pattern therefore
-- produce the SAME id. This fleet already contains qBraid/qBraid and
-- brianpearson-hub/qBraid. Under the old constraint UpsertPatternStats'
-- ON CONFLICT would resolve one tenant's row onto another's and merge their
-- quality counters — silently, with no error and no log. Scoping the constraint
-- to (installation_id, memory_doc_id) restores per-tenant identity.
--
-- decision_traces.supermemory_id is dropped rather than renamed: it has zero
-- writers and zero readers, and decision_traces is already the source of truth
-- for traces. Its partial index existed only to make the reconciler's
-- drift-repair sweep cheap, and that job class is going away with Supermemory.

ALTER TABLE patterns RENAME COLUMN supermemory_id TO memory_doc_id;
ALTER TABLE patterns RENAME COLUMN supermemory_custom_id TO memory_custom_id;
ALTER INDEX IF EXISTS idx_patterns_supermemory_custom_id RENAME TO idx_patterns_memory_custom_id;

ALTER TABLE scenarios RENAME COLUMN supermemory_id TO memory_doc_id;
ALTER INDEX IF EXISTS idx_scenarios_pending_sm RENAME TO idx_scenarios_pending_index;

ALTER TABLE pattern_stats RENAME COLUMN supermemory_id TO memory_doc_id;
-- The constraint name Postgres generated for the inline UNIQUE.
ALTER TABLE pattern_stats DROP CONSTRAINT IF EXISTS pattern_stats_supermemory_id_key;
ALTER TABLE pattern_stats ADD CONSTRAINT pattern_stats_install_doc_key
    UNIQUE (installation_id, memory_doc_id);

DROP INDEX IF EXISTS idx_decision_traces_pending_sm;
ALTER TABLE decision_traces DROP COLUMN IF EXISTS supermemory_id;
