ALTER TABLE scenarios
    DROP COLUMN IF EXISTS last_verdict,
    DROP COLUMN IF EXISTS last_confidence,
    DROP COLUMN IF EXISTS last_why,
    DROP COLUMN IF EXISTS last_fix,
    DROP COLUMN IF EXISTS last_pr_number,
    DROP COLUMN IF EXISTS last_review_id;

DROP TABLE IF EXISTS scenario_runs;
