-- ThreadRegistry (#162): persist the durable link between a posted review
-- finding and its GitHub review-thread identity.
--
-- Before this, the only stored GitHub handle was github_comment_id — a REST
-- comment id, itself backfilled fuzzily by (review_id, file_path, end_line).
-- The GraphQL ReviewThread node id (the handle ResolveReviewThread / dismissal
-- actually need) was never persisted, forcing auto-resolve to re-list every
-- thread on the PR each push and re-match by line proximity.
--
-- Schema choice: a single nullable column on review_comments, NOT a new table.
-- The mapping finding ↔ {thread_node_id, rest_comment_id, path, line} is already
-- 1:1 with a review_comments row — github_comment_id IS the rest_comment_id,
-- file_path IS the path, end_line IS the line. Only the GraphQL node id was
-- missing, so one column completes the mapping with zero joins and no new
-- lifecycle to keep in sync with review_comments' ON DELETE CASCADE.
ALTER TABLE review_comments
    ADD COLUMN IF NOT EXISTS graphql_thread_node_id TEXT;

-- Partial index: consumers resolve a thread BY node id (dismissal → thread) and
-- list hydrated links for a review. Posted-but-unhydrated rows and suppressed
-- findings (node id NULL) dominate and stay out of the index. Backward compat:
-- pre-migration rows and post-time hydrate misses keep node id NULL and fall
-- back to the existing fuzzy github_comment_id path.
CREATE INDEX IF NOT EXISTS idx_review_comments_thread_node
    ON review_comments(graphql_thread_node_id)
    WHERE graphql_thread_node_id IS NOT NULL;
