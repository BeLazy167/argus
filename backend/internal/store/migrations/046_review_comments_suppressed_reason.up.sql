-- Records why a generated finding was suppressed from posting (never shown on
-- the PR, still persisted for the dashboard + analytics). Set by the
-- post-generation dismissal-match pass: 'dismissed_match:<score>' when a finding
-- semantically matches a previously 👎-dismissed finding at/above the drop
-- threshold. NULL = posted normally.
--
-- Squash-safe: nullable, no default, no index — cheap ALTER even on a large
-- populated review_comments table.
ALTER TABLE review_comments ADD COLUMN IF NOT EXISTS suppressed_reason TEXT;
