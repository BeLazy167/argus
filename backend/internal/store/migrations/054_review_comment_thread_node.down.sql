DROP INDEX IF EXISTS idx_review_comments_thread_node;
ALTER TABLE review_comments DROP COLUMN IF EXISTS graphql_thread_node_id;
