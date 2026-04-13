DROP INDEX IF EXISTS idx_installations_clerk_org;
ALTER TABLE installations DROP COLUMN IF EXISTS clerk_org_id;
