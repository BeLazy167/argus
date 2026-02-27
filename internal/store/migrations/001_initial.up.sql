CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- GitHub App installations
CREATE TABLE installations (
    id              BIGSERIAL PRIMARY KEY,
    installation_id BIGINT NOT NULL UNIQUE,
    org_login       TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    suspended_at    TIMESTAMPTZ
);

-- Repos tracked by the bot
CREATE TABLE repos (
    id              BIGSERIAL PRIMARY KEY,
    installation_id BIGINT NOT NULL REFERENCES installations(id),
    github_id       BIGINT NOT NULL UNIQUE,
    full_name       TEXT NOT NULL,
    default_branch  TEXT NOT NULL DEFAULT 'main',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    settings_json   JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Per-repo, per-stage model configuration
CREATE TABLE model_configs (
    id          BIGSERIAL PRIMARY KEY,
    repo_id     BIGINT REFERENCES repos(id),
    stage       TEXT NOT NULL CHECK (stage IN ('triage','review','synthesis','embedding')),
    provider    TEXT NOT NULL,
    model       TEXT NOT NULL,
    base_url    TEXT,
    max_tokens  INT NOT NULL DEFAULT 4096,
    temperature REAL NOT NULL DEFAULT 0.2,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repo_id, stage)
);

-- Org-wide review rules
CREATE TABLE rules (
    id          BIGSERIAL PRIMARY KEY,
    category    TEXT NOT NULL,
    content     TEXT NOT NULL,
    priority    INT NOT NULL DEFAULT 0,
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Reviews (one per PR review run)
CREATE TABLE reviews (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    repo_id          BIGINT NOT NULL REFERENCES repos(id),
    pr_number        INT NOT NULL,
    pr_title         TEXT NOT NULL,
    pr_author        TEXT NOT NULL,
    head_sha         TEXT NOT NULL,
    base_sha         TEXT NOT NULL,
    github_review_id BIGINT,
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','in_progress','completed','failed')),
    summary          TEXT,
    score            INT,
    token_usage      JSONB,
    trigger          TEXT NOT NULL DEFAULT 'webhook'
                     CHECK (trigger IN ('webhook','manual')),
    triggered_by     TEXT,
    duration_ms      INT,
    error            TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at     TIMESTAMPTZ
);
CREATE INDEX idx_reviews_repo_pr ON reviews(repo_id, pr_number);

-- Per-file review comments
CREATE TABLE review_comments (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    review_id   UUID NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    file_path   TEXT NOT NULL,
    start_line  INT,
    end_line    INT,
    side        TEXT CHECK (side IN ('LEFT','RIGHT')),
    body        TEXT NOT NULL,
    severity    TEXT CHECK (severity IN ('critical','warning','suggestion','praise')),
    category    TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_review_comments_review ON review_comments(review_id);

-- Pipeline state machine persistence
CREATE TABLE pipeline_states (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    review_id   UUID NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    state       TEXT NOT NULL,
    payload     JSONB,
    error       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_pipeline_active ON pipeline_states(state) WHERE state NOT IN ('completed','failed');

-- Activity log
CREATE TABLE activity_log (
    id          BIGSERIAL PRIMARY KEY,
    action      TEXT NOT NULL,
    actor       TEXT,
    resource    TEXT,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
