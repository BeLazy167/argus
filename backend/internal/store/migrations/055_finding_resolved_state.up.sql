-- FindingLifecycle (#165): add the 'resolved' terminal state.
--
-- review_comments.state becomes the single source of truth for a finding's
-- terminal lifecycle state, written ONLY by the FindingLifecycle module. It
-- already carried posted/addressed/dismissed/deferred/suppressed (migration
-- 051); this adds 'resolved' for a finding a maintainer closed via
-- `@argus resolve` — an explicit operator "these are handled, close them"
-- signal that is neither an addressed fix nor a dismissal, so folding it into
-- either would poison the gauge's address/dismiss rates. A distinct terminal
-- state keeps the ledger honest.
--
-- The inline column CHECK from 051 is auto-named review_comments_state_check;
-- drop and re-add it with the widened value set.
ALTER TABLE review_comments DROP CONSTRAINT IF EXISTS review_comments_state_check;
ALTER TABLE review_comments ADD CONSTRAINT review_comments_state_check
    CHECK (state IN ('posted','addressed','dismissed','deferred','resolved','suppressed'));
