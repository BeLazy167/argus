-- Production drifted from migration 086. The index it actually created there is
-- named patterns_convention_memory_custom_uniq and carries an extra
-- `source = 'convention'` predicate, so it cannot serve as the arbiter for
-- CreatePattern's `ON CONFLICT (installation_id, memory_custom_id)
-- WHERE memory_custom_id IS NOT NULL`. Every pattern upsert in production has
-- failed with SQLSTATE 42P10 since that index shipped: pipeline auto-learn,
-- convention extraction, scoring-confirmed writes, /argus remember, the
-- dashboard create, and the MCP create_memory tool.
--
-- golang-migrate records only the version number, so editing 086 would never
-- re-run. This migration reconciles both shapes and is idempotent against each:
-- in dev and CI the correct index already exists and the stale one does not.

-- A unique index cannot be built while duplicates exist. Production holds one
-- such pair (two scoring_confirmed rows sharing an identity, written before 086).
-- Keep the lowest id, which is the row CreateOrGetPattern's recheck also selects.
--
-- review_comments.matched_pattern_id is the only foreign key onto patterns and
-- is NO ACTION, so a comment still pointing at a doomed duplicate would abort
-- this migration and with it the deploy. Repoint such comments at the
-- surviving sibling first; the migration must not depend on the referencing
-- rows that happen to exist on the day it runs.
UPDATE review_comments rc
SET matched_pattern_id = (
  SELECT min(q.id)
  FROM patterns q
  WHERE q.installation_id = dup.installation_id
    AND q.memory_custom_id = dup.memory_custom_id
)
FROM patterns dup
WHERE rc.matched_pattern_id = dup.id
  AND dup.memory_custom_id IS NOT NULL
  AND EXISTS (
    SELECT 1
    FROM patterns q
    WHERE q.installation_id = dup.installation_id
      AND q.memory_custom_id = dup.memory_custom_id
      AND q.id < dup.id
  );

DELETE FROM patterns p
USING patterns q
WHERE p.memory_custom_id IS NOT NULL
  AND q.memory_custom_id IS NOT NULL
  AND p.installation_id = q.installation_id
  AND p.memory_custom_id = q.memory_custom_id
  AND p.id > q.id;

CREATE UNIQUE INDEX IF NOT EXISTS patterns_installation_memory_custom_uniq
  ON patterns (installation_id, memory_custom_id)
  WHERE memory_custom_id IS NOT NULL;

-- Fully redundant once the broader index exists: uniqueness over every identity
-- implies uniqueness within conventions.
DROP INDEX IF EXISTS patterns_convention_memory_custom_uniq;
