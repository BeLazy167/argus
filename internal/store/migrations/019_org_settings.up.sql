-- Org-level default settings (persona, feature toggles, etc.)
ALTER TABLE installations ADD COLUMN IF NOT EXISTS default_settings JSONB DEFAULT '{}';
