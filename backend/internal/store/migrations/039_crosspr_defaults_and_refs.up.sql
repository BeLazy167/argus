-- Migration 039: enables fast sibling-refresh reverse lookup for cross-PR
-- checks, and documents the default flip from cross_pr_checks=off to
-- cross_pr_checks=on for NEW installations.
--
-- Existing installations are UNTOUCHED — they keep whatever toggle value
-- their settings_json / feature_flags currently holds. This is the
-- conservative rollout: new installs get the feature on; existing
-- customers opt in via settings.
--
-- Default flag flip location: done in Go (pipeline.DefaultFeatureFlags),
-- NOT in SQL. The installations.feature_flags column has a static
-- DEFAULT '{}'::jsonb (see 030_feature_flags.up.sql); the loader in
-- backend/internal/pipeline/acceptance.go:loadFeatureFlags falls back to
-- DefaultFeatureFlags() whenever the stored JSON is empty or missing the
-- cross_pr_checks key. Flipping the Go default is therefore both
-- sufficient and safe — it cannot disturb existing rows whose
-- feature_flags explicitly record {"cross_pr_checks": false, ...}
-- (backfilled by migration 030).
--
-- linked_pr_refs: JSONB array of {owner, repo, number} populated at
-- synthesis-completion time. GIN index with jsonb_path_ops makes the
-- "who links to PR X?" reverse-lookup query fast under containment.
ALTER TABLE reviews
    ADD COLUMN linked_pr_refs JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX reviews_linked_pr_refs_gin
    ON reviews USING GIN (linked_pr_refs jsonb_path_ops);
