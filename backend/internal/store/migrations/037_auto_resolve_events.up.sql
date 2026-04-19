-- Auto-resolve events: one row per synchronize push that ran auto-resolve
-- and touched at least one thread. Populated by the fire-and-forget goroutine
-- spawned on synchronize webhooks (see Orchestrator.autoResolveOnSynchronize).
--
-- Powers the "Automated hygiene" card on the stats page and lets users
-- reconcile "why did Argus close that thread" with a specific source_sha.
--
-- No foreign key to reviews: auto-resolve runs even when no review follows
-- (auto_run=false repos), so we key purely on (installation, repo, pr, sha).
-- A single push may produce at most one row; multiple rapid pushes produce
-- multiple rows (intentional — each push is its own event).
CREATE TABLE auto_resolve_events (
    id               BIGSERIAL   PRIMARY KEY,
    installation_id  BIGINT      NOT NULL REFERENCES installations(id) ON DELETE CASCADE,
    repo_id          BIGINT      NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    pr_number        INT         NOT NULL,
    source_sha       TEXT        NOT NULL,
    resolved_count   INT         NOT NULL CHECK (resolved_count >= 0),
    attempted_count  INT         NOT NULL CHECK (attempted_count >= 0),
    github_api_calls INT         NOT NULL DEFAULT 0 CHECK (github_api_calls >= 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Every resolve counts as one attempt, so resolved can never exceed
    -- attempted. Guards against orchestrator regressions silently
    -- corrupting the event stream.
    CHECK (resolved_count <= attempted_count),
    -- One event per push SHA per PR. GitHub retries delivery of webhook
    -- events on failure; without this, a retried synchronize would
    -- double-count auto-resolve activity in the stats. The goroutine's
    -- INSERT uses ON CONFLICT DO NOTHING, so retries become a no-op.
    -- A legitimate force-push-to-same-SHA would also dedupe here — that
    -- edge case is intentional (the diff is identical, the metrics
    -- would match).
    UNIQUE (installation_id, repo_id, pr_number, source_sha)
);
CREATE INDEX idx_auto_resolve_events_installation
    ON auto_resolve_events (installation_id, created_at DESC);
CREATE INDEX idx_auto_resolve_events_pr
    ON auto_resolve_events (repo_id, pr_number);
