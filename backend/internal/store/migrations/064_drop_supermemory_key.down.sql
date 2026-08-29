-- Restores the column, not its contents. The keys are unrecoverable by design;
-- a rollback that needs them must re-enter them.
ALTER TABLE installations ADD COLUMN IF NOT EXISTS supermemory_key_enc TEXT;
