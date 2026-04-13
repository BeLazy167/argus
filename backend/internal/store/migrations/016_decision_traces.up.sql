CREATE TABLE decision_traces (
    id BIGSERIAL PRIMARY KEY,
    repo_id BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    file_path TEXT NOT NULL,
    symbol_name TEXT, -- function/class name, null if file-level
    trace_type TEXT NOT NULL CHECK (trace_type IN (
        'review_finding',    -- Argus found an issue
        'developer_agreed',  -- dev accepted the finding
        'developer_dismissed', -- dev dismissed the finding
        'pattern_matched',   -- matched a known pattern
        'bug_reported',      -- linked to a bug report
        'fix_applied',       -- code was fixed based on finding
        'config_changed',    -- configuration was modified
        'incident'           -- linked to a production incident
    )),
    content TEXT NOT NULL,
    severity TEXT,
    review_id UUID REFERENCES reviews(id),
    pr_number INT,
    metadata JSONB DEFAULT '{}', -- flexible extra data (pattern_id, issue_url, etc)
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_traces_repo_file ON decision_traces(repo_id, file_path);
CREATE INDEX idx_traces_repo_created ON decision_traces(repo_id, created_at DESC);
CREATE INDEX idx_traces_review ON decision_traces(review_id);
