CREATE TABLE patterns (
  id SERIAL PRIMARY KEY,
  installation_id BIGINT NOT NULL REFERENCES installations(id),
  repo_id BIGINT REFERENCES repos(id),
  content TEXT NOT NULL,
  supermemory_id TEXT,
  created_by TEXT,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_patterns_installation ON patterns(installation_id);

ALTER TABLE reviews ADD COLUMN IF NOT EXISTS file_count INT;
