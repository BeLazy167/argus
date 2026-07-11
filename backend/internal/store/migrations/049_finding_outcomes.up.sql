-- Gauge (address-rate telemetry), part 1: outcome vocabulary + timestamp.
--
-- comment_outcomes already stores per-finding signals ('confirmed' via 👍,
-- 'dismissed' via 👎, 'ignored'). The Gauge adds merge-time outcomes written
-- by the PR-closed detection job:
--   addressed_human — code changed within ±3 lines of the finding anchor by a
--                     human author after the comment was posted
--   addressed_agent — same, but the fixing commit author matches a bot/agent
--                     login pattern (counted at half weight in the view)
--   deferred        — PR closed without merging
-- ('ignored' is reused for merged-without-addressing.)
--
-- Anchor (file_path + end_line), category, and posted_at already live on
-- review_comments; change_class lives in reviews.review_contract — no
-- duplication here, the vw_review_gauge view (migration 050) joins them.

ALTER TABLE comment_outcomes DROP CONSTRAINT IF EXISTS comment_outcomes_outcome_check;
ALTER TABLE comment_outcomes ADD CONSTRAINT comment_outcomes_outcome_check
    CHECK (outcome IN ('confirmed','dismissed','ignored','addressed_human','addressed_agent','deferred'));

-- When the detection job concluded the finding was addressed (merge sweep
-- time). NULL for reaction-driven outcomes. created_at stays "row written".
ALTER TABLE comment_outcomes ADD COLUMN IF NOT EXISTS addressed_at TIMESTAMPTZ;
