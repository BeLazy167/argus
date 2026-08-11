-- Full graph generations are staged at one immutable commit and become visible
-- only after every source file has a successful staged snapshot.
CREATE TABLE graph_index_generations (
    id BIGSERIAL PRIMARY KEY,
    repo_id BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    commit_sha TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('building', 'published', 'failed', 'superseded')),
    tree_truncated BOOLEAN NOT NULL DEFAULT false,
    expected_files INTEGER NOT NULL DEFAULT 0 CHECK (expected_files >= 0),
    visited_files INTEGER NOT NULL DEFAULT 0 CHECK (visited_files >= 0),
    failed_files INTEGER NOT NULL DEFAULT 0 CHECK (failed_files >= 0),
    skipped_files INTEGER NOT NULL DEFAULT 0 CHECK (skipped_files >= 0),
    error TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX graph_index_one_building_per_repo
    ON graph_index_generations (repo_id) WHERE status = 'building';
CREATE INDEX graph_index_generations_repo_started
    ON graph_index_generations (repo_id, started_at DESC);

CREATE TABLE graph_index_generation_files (
    generation_id BIGINT NOT NULL REFERENCES graph_index_generations(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('ready', 'failed')),
    symbols JSONB NOT NULL DEFAULT '[]'::jsonb,
    edges JSONB NOT NULL DEFAULT '[]'::jsonb,
    endpoints JSONB NOT NULL DEFAULT '[]'::jsonb,
    error TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (generation_id, file_path)
);

ALTER TABLE repos ADD COLUMN graph_published_generation_id BIGINT
    REFERENCES graph_index_generations(id) ON DELETE SET NULL;
ALTER TABLE repos ADD COLUMN graph_index_commit_sha TEXT;
ALTER TABLE repos ADD COLUMN graph_index_expected_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repos ADD COLUMN graph_index_visited_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repos ADD COLUMN graph_index_failed_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repos ADD COLUMN graph_index_skipped_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repos ADD COLUMN graph_index_tree_truncated BOOLEAN NOT NULL DEFAULT false;
