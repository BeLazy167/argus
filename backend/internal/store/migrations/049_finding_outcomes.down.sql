DELETE FROM comment_outcomes WHERE outcome IN ('addressed_human','addressed_agent','deferred');
ALTER TABLE comment_outcomes DROP CONSTRAINT IF EXISTS comment_outcomes_outcome_check;
ALTER TABLE comment_outcomes ADD CONSTRAINT comment_outcomes_outcome_check
    CHECK (outcome IN ('confirmed','dismissed','ignored'));
ALTER TABLE comment_outcomes DROP COLUMN IF EXISTS addressed_at;
