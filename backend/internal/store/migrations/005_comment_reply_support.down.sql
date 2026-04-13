DROP INDEX IF EXISTS idx_review_comments_github_id;
ALTER TABLE review_comments DROP COLUMN github_comment_id;
