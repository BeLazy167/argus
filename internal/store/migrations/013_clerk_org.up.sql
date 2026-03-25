ALTER TABLE installations ADD COLUMN IF NOT EXISTS clerk_org_id TEXT;
CREATE INDEX IF NOT EXISTS idx_installations_clerk_org ON installations(clerk_org_id);
