-- LLM-produced architecture annotations are hypotheses, not parser-owned code facts.
-- Keeping them in separate tables prevents an annotation from overwriting a
-- deterministic node's line range, content hash, or edge set.
CREATE TABLE architecture_annotation_nodes (
    id BIGSERIAL PRIMARY KEY,
    repo_id BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    pr_number INTEGER NOT NULL,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    file_path TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repo_id, pr_number, file_path, kind, name)
);
CREATE INDEX architecture_annotation_nodes_repo_pr
    ON architecture_annotation_nodes (repo_id, pr_number);

CREATE TABLE architecture_annotation_edges (
    id BIGSERIAL PRIMARY KEY,
    repo_id BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    pr_number INTEGER NOT NULL,
    source_id BIGINT NOT NULL REFERENCES architecture_annotation_nodes(id) ON DELETE CASCADE,
    target_id BIGINT NOT NULL REFERENCES architecture_annotation_nodes(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repo_id, pr_number, source_id, target_id, kind)
);
CREATE INDEX architecture_annotation_edges_repo_pr
    ON architecture_annotation_edges (repo_id, pr_number);
