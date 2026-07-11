ALTER TABLE review_comments DROP COLUMN IF EXISTS state;
DELETE FROM comment_outcomes WHERE outcome = 'not_applicable_change_kind';
-- Restore 049_finding_outcomes' constraint (union minus not_applicable_change_kind).
ALTER TABLE comment_outcomes DROP CONSTRAINT IF EXISTS comment_outcomes_outcome_check;
ALTER TABLE comment_outcomes ADD CONSTRAINT comment_outcomes_outcome_check
    CHECK (outcome IN ('confirmed','dismissed','ignored',
                       'addressed_human','addressed_agent','deferred'));
