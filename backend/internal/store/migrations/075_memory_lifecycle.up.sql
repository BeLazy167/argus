-- Memory lifecycle, append-only review attribution, and durable relational mirrors.
--
-- live_memories is the single read contract for usable knowledge. Readers use
-- the view instead of maintaining independent tombstone clauses that drift as
-- lifecycle states are added.
CREATE OR REPLACE VIEW live_memories AS
SELECT *
FROM memories
WHERE deleted_at IS NULL
  AND invalidated_at IS NULL
  AND superseded_by IS NULL;

-- A memory row is mutable current state, but the set of reviews that learned it
-- is history. Keep the current writer on memories.review_id for provenance and
-- record every review attribution here without transferring ownership away
-- from earlier reviews on deterministic re-upsert.
CREATE TABLE memory_review_attributions (
  memory_id     bigint      NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
  review_id     uuid        NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
  attributed_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (memory_id, review_id)
);

CREATE INDEX memory_review_attributions_review_idx
  ON memory_review_attributions (review_id, attributed_at DESC, memory_id);

-- Preserve the best attribution available for rows written since migration
-- 070. updated_at is the closest available approximation of their write time.
INSERT INTO memory_review_attributions (memory_id, review_id, attributed_at)
SELECT id, review_id, updated_at
FROM memories
WHERE review_id IS NOT NULL
ON CONFLICT (memory_id, review_id) DO NOTHING;

-- Pattern/rule mutations enqueue their memory mirror transition in the same
-- relational transaction. The payload carries all identity needed after a
-- delete; every transition remains replayable, so this table deliberately has
-- no coalescing uniqueness constraint.
CREATE TABLE memory_mirror_outbox (
  id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  installation_id bigint      NOT NULL REFERENCES installations(id),
  aggregate_type text          NOT NULL CHECK (aggregate_type IN ('pattern', 'rule')),
  aggregate_id   bigint        NOT NULL,
  operation      text          NOT NULL CHECK (operation IN ('upsert', 'delete')),
  payload        jsonb         NOT NULL DEFAULT '{}'::jsonb,
  attempt_count  integer       NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  available_at   timestamptz   NOT NULL DEFAULT now(),
  claimed_at     timestamptz,
  processed_at   timestamptz,
  last_error     text,
  created_at     timestamptz   NOT NULL DEFAULT now(),
  updated_at     timestamptz   NOT NULL DEFAULT now()
);

-- Supports both ready work (claimed_at IS NULL) and stale-claim recovery while
-- keeping completed history out of the worker scan.
CREATE INDEX memory_mirror_outbox_pending_idx
  ON memory_mirror_outbox (available_at, claimed_at, id)
  WHERE processed_at IS NULL;

-- Keep the btree live-row scope aligned with the view. The vector index's older
-- predicate remains usable because the view implies it; rebuilding it is an
-- operator-specific concern when production owns the column with pgContext.
DROP INDEX IF EXISTS memories_scope_idx;
CREATE INDEX memories_scope_idx ON memories (installation_id, container_tag, type)
  WHERE deleted_at IS NULL AND invalidated_at IS NULL AND superseded_by IS NULL;
