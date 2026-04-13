DROP INDEX IF EXISTS idx_model_configs_org;
ALTER TABLE model_configs DROP COLUMN IF EXISTS installation_id;
