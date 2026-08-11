DROP INDEX IF EXISTS repos_graph_refresh_due;
ALTER TABLE graph_index_generations DROP COLUMN IF EXISTS refresh_version;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_refresh_version;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_default_head_event_at;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_default_head_observed_at;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_default_head_sha;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_refresh_commit_sha;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_refresh_requested_at;
