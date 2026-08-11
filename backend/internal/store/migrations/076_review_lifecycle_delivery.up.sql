-- Durable review lifecycle delivery, retry generations, and recovery ownership.

CREATE TABLE review_events (
    id          BIGSERIAL PRIMARY KEY,
    review_id   UUID NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,
    data        JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_review_events_replay ON review_events(review_id, id);

ALTER TABLE pipeline_states
    ADD COLUMN recovery_owner UUID,
    ADD COLUMN recovery_lease_until TIMESTAMPTZ;
CREATE INDEX idx_pipeline_recovery_claim
    ON pipeline_states(updated_at, recovery_lease_until)
    WHERE state NOT IN ('completed', 'failed', 'cancelled');

ALTER TABLE reviews
    ADD COLUMN attempt_generation INT NOT NULL DEFAULT 1
    CHECK (attempt_generation > 0);
ALTER TABLE review_comments
    ADD COLUMN attempt_generation INT NOT NULL DEFAULT 1
    CHECK (attempt_generation > 0);
CREATE INDEX idx_review_comments_current_attempt
    ON review_comments(review_id, attempt_generation);

CREATE TABLE review_signals (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    repo_id     BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    pr_number   INT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('auto_run_disabled')),
    claimed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repo_id, pr_number, kind)
);

-- Synthetic failure rows were delivery markers, never review attempts.
INSERT INTO review_signals (repo_id, pr_number, kind, claimed_at, delivered_at, created_at)
SELECT DISTINCT ON (repo_id, pr_number)
       repo_id, pr_number, 'auto_run_disabled', created_at, created_at, created_at
FROM reviews
WHERE github_review_id IS NULL
  AND status = 'failed'
  AND error = 'auto_run_disabled'
ORDER BY repo_id, pr_number, created_at;
DELETE FROM reviews
WHERE github_review_id IS NULL
  AND status = 'failed'
  AND error = 'auto_run_disabled';
