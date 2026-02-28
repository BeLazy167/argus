ALTER TABLE review_comments ADD COLUMN github_comment_id BIGINT;
CREATE INDEX idx_review_comments_github_id ON review_comments(github_comment_id) WHERE github_comment_id IS NOT NULL;
