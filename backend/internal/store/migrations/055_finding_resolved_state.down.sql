-- Revert the 'resolved' terminal state (migration 055). Fails if any row is
-- currently in 'resolved'; that is intentional — an operator would need to
-- reconcile those rows before narrowing the constraint.
ALTER TABLE review_comments DROP CONSTRAINT IF EXISTS review_comments_state_check;
ALTER TABLE review_comments ADD CONSTRAINT review_comments_state_check
    CHECK (state IN ('posted','addressed','dismissed','deferred','suppressed'));
