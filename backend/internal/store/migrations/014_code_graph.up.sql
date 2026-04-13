CREATE TABLE code_nodes (
    id BIGSERIAL PRIMARY KEY,
    repo_id BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('function','method','class','type','interface','file','module')),
    name TEXT NOT NULL,
    file_path TEXT NOT NULL,
    line_start INT,
    line_end INT,
    language TEXT,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_code_nodes_repo ON code_nodes(repo_id);
CREATE INDEX idx_code_nodes_file ON code_nodes(repo_id, file_path);
CREATE UNIQUE INDEX idx_code_nodes_unique ON code_nodes(repo_id, file_path, kind, name);

CREATE TABLE code_edges (
    id BIGSERIAL PRIMARY KEY,
    repo_id BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    source_id BIGINT NOT NULL REFERENCES code_nodes(id) ON DELETE CASCADE,
    target_id BIGINT NOT NULL REFERENCES code_nodes(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('calls','imports','inherits','implements','uses_type')),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_code_edges_source ON code_edges(source_id);
CREATE INDEX idx_code_edges_target ON code_edges(target_id);
CREATE UNIQUE INDEX idx_code_edges_unique ON code_edges(repo_id, source_id, target_id, kind);
