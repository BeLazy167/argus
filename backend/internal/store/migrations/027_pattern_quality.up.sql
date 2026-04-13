CREATE TABLE IF NOT EXISTS pattern_stats (
    id              BIGSERIAL PRIMARY KEY,
    installation_id BIGINT NOT NULL,
    repo_id         BIGINT,
    supermemory_id  TEXT NOT NULL UNIQUE,
    content_hash    TEXT NOT NULL,
    category        TEXT NOT NULL DEFAULT '',
    times_matched   INT NOT NULL DEFAULT 0,
    times_confirmed INT NOT NULL DEFAULT 0,
    times_dismissed INT NOT NULL DEFAULT 0,
    quality_score   FLOAT NOT NULL DEFAULT 0.5,
    last_matched_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_pattern_stats_install ON pattern_stats(installation_id);
CREATE INDEX IF NOT EXISTS idx_pattern_stats_quality ON pattern_stats(quality_score);
