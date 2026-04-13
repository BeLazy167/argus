ALTER TABLE model_configs ADD COLUMN IF NOT EXISTS installation_id BIGINT REFERENCES installations(id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_model_configs_org ON model_configs (installation_id, stage) WHERE repo_id IS NULL AND installation_id IS NOT NULL;
