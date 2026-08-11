DROP TABLE IF EXISTS review_signals;
DROP INDEX IF EXISTS idx_review_comments_current_attempt;
ALTER TABLE review_comments DROP COLUMN IF EXISTS attempt_generation;
ALTER TABLE reviews DROP COLUMN IF EXISTS attempt_generation;
DROP INDEX IF EXISTS idx_pipeline_recovery_claim;
ALTER TABLE pipeline_states
    DROP COLUMN IF EXISTS recovery_lease_until,
    DROP COLUMN IF EXISTS recovery_owner;
DROP TABLE IF EXISTS review_events;
