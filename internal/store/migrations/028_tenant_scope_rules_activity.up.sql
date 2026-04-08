-- Add installation_id to rules for tenant scoping
ALTER TABLE rules ADD COLUMN IF NOT EXISTS installation_id BIGINT REFERENCES installations(id);
CREATE INDEX IF NOT EXISTS idx_rules_installation ON rules(installation_id);

-- Add installation_id to activity_log for tenant scoping
ALTER TABLE activity_log ADD COLUMN IF NOT EXISTS installation_id BIGINT REFERENCES installations(id);
CREATE INDEX IF NOT EXISTS idx_activity_log_installation ON activity_log(installation_id);

-- Backfill existing rows with installation_id = 1 (single tenant)
UPDATE rules SET installation_id = 1 WHERE installation_id IS NULL;
UPDATE activity_log SET installation_id = 1 WHERE installation_id IS NULL;
