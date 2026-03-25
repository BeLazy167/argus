CREATE TABLE prompt_templates (
  id SERIAL PRIMARY KEY,
  repo_id BIGINT NOT NULL REFERENCES repos(id),
  stage TEXT NOT NULL,
  prompt_text TEXT NOT NULL,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW(),
  UNIQUE(repo_id, stage)
);
