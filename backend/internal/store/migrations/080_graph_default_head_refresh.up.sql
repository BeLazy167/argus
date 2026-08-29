-- A signed default-branch webhook makes the authoritative graph due immediately.
-- The observed head is persisted so API/UI freshness never needs a GitHub call.
ALTER TABLE repos ADD COLUMN graph_refresh_requested_at TIMESTAMPTZ;
ALTER TABLE repos ADD COLUMN graph_refresh_commit_sha TEXT;
ALTER TABLE repos ADD COLUMN graph_default_head_sha TEXT;
ALTER TABLE repos ADD COLUMN graph_default_head_observed_at TIMESTAMPTZ;
ALTER TABLE repos ADD COLUMN graph_default_head_event_at TIMESTAMPTZ;
ALTER TABLE repos ADD COLUMN graph_refresh_version BIGINT NOT NULL DEFAULT 0;

-- A generation remembers the request version captured before resolving the
-- mutable branch. Publication may clear only that version; a later webhook
-- remains due even if it arrived while the immutable generation was building.
ALTER TABLE graph_index_generations ADD COLUMN refresh_version BIGINT NOT NULL DEFAULT 0;

CREATE INDEX repos_graph_refresh_due
    ON repos (graph_refresh_requested_at, graph_index_attempted_at)
    WHERE enabled AND graph_refresh_requested_at IS NOT NULL;
