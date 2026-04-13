CREATE TABLE scenarios (
    id BIGSERIAL PRIMARY KEY,
    installation_id BIGINT NOT NULL REFERENCES installations(id) ON DELETE CASCADE,
    repo_id BIGINT REFERENCES repos(id) ON DELETE CASCADE,
    description TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('review','issue','incident','manual','pattern')),
    source_ref TEXT,
    files TEXT[],
    modules TEXT[],
    severity TEXT CHECK (severity IN ('critical','high','medium','low')),
    active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_scenarios_repo ON scenarios(repo_id) WHERE active = TRUE;
CREATE INDEX idx_scenarios_installation ON scenarios(installation_id) WHERE active = TRUE;
