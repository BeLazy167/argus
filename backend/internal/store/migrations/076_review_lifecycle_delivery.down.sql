-- Old binaries use failed review rows as the durable dedup key for the
-- auto-run-disabled affordance. Recreate that representation before removing
-- review_signals so rolling back does not make an old binary post it again.
-- A prior/partial restore may already have a marker; match the old reader's
-- predicate so one signal never creates a duplicate marker.
INSERT INTO reviews (
    repo_id,
    pr_number,
    pr_title,
    pr_author,
    head_sha,
    base_sha,
    status,
    trigger,
    error,
    created_at,
    completed_at
)
SELECT signals.repo_id,
       signals.pr_number,
       'Auto-run disabled',
       'argus',
       '',
       '',
       'failed',
       'webhook',
       'auto_run_disabled',
       signals.created_at,
       signals.delivered_at
FROM review_signals AS signals
WHERE signals.kind = 'auto_run_disabled'
  AND NOT EXISTS (
      SELECT 1
      FROM reviews AS existing
      WHERE existing.repo_id = signals.repo_id
        AND existing.pr_number = signals.pr_number
        AND existing.status = 'failed'
        AND existing.error = 'auto_run_disabled'
  );

DROP TABLE IF EXISTS review_signals;
DROP INDEX IF EXISTS idx_review_comments_current_attempt;
ALTER TABLE review_comments DROP COLUMN IF EXISTS attempt_generation;
ALTER TABLE reviews DROP COLUMN IF EXISTS attempt_generation;
DROP INDEX IF EXISTS idx_pipeline_recovery_claim;
ALTER TABLE pipeline_states
    DROP COLUMN IF EXISTS recovery_lease_until,
    DROP COLUMN IF EXISTS recovery_owner;
DROP TABLE IF EXISTS review_events;
