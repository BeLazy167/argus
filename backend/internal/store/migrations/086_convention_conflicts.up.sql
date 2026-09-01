-- Convention lifecycle state is authoritative in memories. These tables add
-- idempotent evidence, open-conflict edges, and post-review artifact delivery.
-- Historical pattern writes appended rows under a non-unique custom-id index.
-- Converge duplicates before installing the upsert arbiter. Preserve finding
-- attribution by repointing matched_pattern_id to the newest surviving row.
WITH ranked AS (
  SELECT id, first_value(id) OVER (
    PARTITION BY installation_id, memory_custom_id
    ORDER BY updated_at DESC NULLS LAST, created_at DESC NULLS LAST, id DESC
  ) AS keep_id,
  row_number() OVER (
    PARTITION BY installation_id, memory_custom_id
    ORDER BY updated_at DESC NULLS LAST, created_at DESC NULLS LAST, id DESC
  ) AS rn
  FROM patterns WHERE memory_custom_id IS NOT NULL AND source = 'convention'
), moved AS (
  UPDATE review_comments rc SET matched_pattern_id = ranked.keep_id
  FROM ranked WHERE ranked.rn > 1 AND rc.matched_pattern_id = ranked.id
)
DELETE FROM patterns p USING ranked
WHERE ranked.rn > 1 AND p.id = ranked.id;

CREATE UNIQUE INDEX patterns_convention_memory_custom_uniq
  ON patterns (installation_id, memory_custom_id)
  WHERE memory_custom_id IS NOT NULL AND source = 'convention';

CREATE TABLE convention_evidence (
  convention_memory_id bigint NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
  repo_id bigint NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
  pr_number integer NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (convention_memory_id, repo_id, pr_number)
);

CREATE TABLE convention_conflicts (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  installation_id bigint NOT NULL REFERENCES installations(id),
  repo_id bigint REFERENCES repos(id),
  category text NOT NULL,
  memory_low_id bigint NOT NULL REFERENCES memories(id),
  memory_high_id bigint NOT NULL REFERENCES memories(id),
  introducing_memory_id bigint NOT NULL REFERENCES memories(id),
  state text NOT NULL DEFAULT 'open' CHECK (state IN ('open','resolved_new','resolved_old')),
  introducing_pr integer NOT NULL,
  artifact_node_id text,
  artifact_comment_id bigint,
  delivered_at timestamptz,
  claimed_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (memory_low_id < memory_high_id),
  UNIQUE (memory_low_id, memory_high_id)
);
CREATE INDEX convention_conflicts_delivery_idx
  ON convention_conflicts (installation_id, repo_id, introducing_pr, id)
  WHERE state = 'open' AND delivered_at IS NULL;

-- Open disputes are not enforceable. They remain stored for audit and render
-- through a dedicated read, but ordinary reviewer retrieval excludes both ends.
CREATE OR REPLACE VIEW live_memories AS
SELECT m.*
FROM memories m
WHERE m.deleted_at IS NULL
  AND m.invalidated_at IS NULL
  AND m.superseded_by IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM convention_conflicts c
    WHERE c.state = 'open'
      AND (c.memory_low_id = m.id OR c.memory_high_id = m.id)
  );
