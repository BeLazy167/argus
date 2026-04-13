-- Feature flag support for per-installation toggles (issue acceptance, cross-PR checks, linked-PR cap).
-- Opt-in: cross-PR off by default (costs an extra LLM call); acceptance on by default (cheap + high value).
ALTER TABLE installations ADD COLUMN IF NOT EXISTS feature_flags JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Backfill existing rows with default values so the app doesn't see a mix of {} and real configs.
UPDATE installations
SET feature_flags = '{"cross_pr_checks": false, "issue_acceptance": true, "max_linked_prs": 5}'::jsonb
WHERE feature_flags = '{}'::jsonb OR feature_flags IS NULL;
