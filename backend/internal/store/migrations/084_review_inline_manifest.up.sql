-- Exact inline-delivery manifests and semantic completion outbox rows.
ALTER TABLE reviews
    ADD COLUMN expected_github_inline_count INT
    CHECK (expected_github_inline_count >= 0);

ALTER TABLE review_comments
    ADD COLUMN was_posted_inline BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE review_events
    ADD COLUMN semantic_key TEXT;

CREATE UNIQUE INDEX idx_review_events_attempt_semantic
    ON review_events(review_id, attempt_generation, semantic_key)
    WHERE semantic_key IS NOT NULL;

-- New manifest-owned bindings are 1:1. Legacy rows remain outside this index
-- because older same-anchor backfills may already contain duplicate IDs.
CREATE UNIQUE INDEX idx_review_comments_manifest_github_binding
    ON review_comments(review_id, attempt_generation, github_comment_id)
    WHERE was_posted_inline AND github_comment_id IS NOT NULL;
