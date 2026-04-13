CREATE TABLE user_installations (
    id              BIGSERIAL PRIMARY KEY,
    clerk_user_id   TEXT NOT NULL,
    installation_id BIGINT NOT NULL REFERENCES installations(id) ON DELETE CASCADE,
    role            TEXT NOT NULL DEFAULT 'owner',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (clerk_user_id, installation_id)
);
CREATE INDEX idx_user_installations_user ON user_installations(clerk_user_id);

ALTER TABLE repos ALTER COLUMN enabled SET DEFAULT FALSE;
