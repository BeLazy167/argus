-- Follow-up ledger: lifecycle state per posted finding.
--   posted     — shipped to the PR, no outcome yet (default)
--   addressed  — the flagged code was fixed (set by PR4's detector)
--   dismissed  — developer rejected it (reply analysis / 👎 reaction)
--   deferred   — acknowledged but not fixed at merge time
--   suppressed — never posted (dismissal-match / team-feedback suppression)
ALTER TABLE review_comments
    ADD COLUMN IF NOT EXISTS state TEXT NOT NULL DEFAULT 'posted'
    CHECK (state IN ('posted','addressed','dismissed','deferred','suppressed'));

-- Backfill from what we already know: suppressed_reason marks findings the
-- suppression pass dropped pre-post; comment_outcomes carries dismissals
-- recorded by the reply/reaction analyzers before this column existed.
UPDATE review_comments SET state = 'suppressed' WHERE suppressed_reason IS NOT NULL;
UPDATE review_comments rc SET state = 'dismissed'
FROM comment_outcomes co
WHERE co.review_comment_id = rc.id AND co.outcome = 'dismissed' AND rc.state = 'posted';

-- Partial index: ledger queries filter on non-default states; 'posted' rows
-- dominate and stay out of the index.
CREATE INDEX IF NOT EXISTS idx_review_comments_state
    ON review_comments(state) WHERE state <> 'posted';

-- Outcome taxonomy gains not_applicable_change_kind: valid finding, wrong
-- change kind (e.g. flagged production rigor on a one-off script).
-- Union with the Gauge outcomes added in 049 — this migration runs after it
-- and must not drop addressed_human/addressed_agent/deferred.
ALTER TABLE comment_outcomes DROP CONSTRAINT IF EXISTS comment_outcomes_outcome_check;
ALTER TABLE comment_outcomes ADD CONSTRAINT comment_outcomes_outcome_check
    CHECK (outcome IN ('confirmed','dismissed','ignored','not_applicable_change_kind',
                       'addressed_human','addressed_agent','deferred'));
