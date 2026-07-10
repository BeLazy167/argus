-- Prevent duplicate comment_outcomes rows from GitHub webhook retries.
-- Before this migration, reaction.created events firing twice for the same
-- reaction produced two identical rows, skewing aggregations like
-- "how often is this finding dismissed?". The ON CONFLICT clause in the
-- query + this UNIQUE constraint make the write idempotent.
--
-- Safe to apply: any existing duplicate rows would already be indistinguishable
-- in meaning, so we dedupe them first, then add the constraint.

DELETE FROM comment_outcomes a
USING comment_outcomes b
WHERE a.ctid < b.ctid
  AND a.review_comment_id = b.review_comment_id
  AND a.outcome = b.outcome;

ALTER TABLE comment_outcomes
    ADD CONSTRAINT comment_outcomes_unique_per_comment
    UNIQUE (review_comment_id, outcome);
