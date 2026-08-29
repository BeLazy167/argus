DROP INDEX IF EXISTS idx_review_comments_manifest_github_binding;
DROP INDEX IF EXISTS idx_review_events_attempt_semantic;
ALTER TABLE review_events DROP COLUMN IF EXISTS semantic_key;
ALTER TABLE review_comments DROP COLUMN IF EXISTS was_posted_inline;
ALTER TABLE reviews DROP COLUMN IF EXISTS expected_github_inline_count;
