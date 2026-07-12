-- Add a durable customId mirror to patterns so the per-finding memory-enrich
-- read can resolve a Supermemory search hit back to its patterns row by the
-- deterministic customId. /v4/search results carry metadata (the write path
-- mirrors custom_id there) but no top-level customId, and a hybrid hit's own id
-- may be a chunk id that never matches the stored supermemory_id — so keying on
-- supermemory_id alone silently misses.
--
-- Nullable: legacy rows stay NULL. Backfill is intentionally omitted — the
-- customId is a sha256 over normalized content plus a repo/source segment that
-- is NOT reconstructable from the stored columns alone (patterns holds repo_id,
-- not the repo short name, and the customId's source segment differs from the
-- stored source, e.g. "confirmed" vs "scoring_confirmed"). New writes populate
-- it; legacy rows fall back to the supermemory_id match at read time.

ALTER TABLE patterns ADD COLUMN IF NOT EXISTS supermemory_custom_id TEXT;

CREATE INDEX IF NOT EXISTS idx_patterns_supermemory_custom_id
    ON patterns (supermemory_custom_id)
    WHERE supermemory_custom_id IS NOT NULL;
