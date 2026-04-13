CREATE TABLE provider_keys (
    id              BIGSERIAL PRIMARY KEY,
    installation_id BIGINT NOT NULL REFERENCES installations(id),
    repo_id         BIGINT,
    provider        TEXT NOT NULL,
    api_key_enc     TEXT NOT NULL,
    base_url        TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(installation_id, repo_id, provider)
);
CREATE UNIQUE INDEX provider_keys_org_level ON provider_keys (installation_id, provider) WHERE repo_id IS NULL;
