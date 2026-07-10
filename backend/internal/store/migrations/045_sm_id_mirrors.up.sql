-- Add supermemory_id mirror columns to scenarios and decision_traces so the
-- reconciler (cmd/reconcile-memory) can find PG rows that failed to index in
-- Supermemory and retry the write. patterns already has this column since
-- migration 006.
--
-- Nullable: a row without supermemory_id is pending reconciliation. Partial
-- indexes skip rows that have successfully synced to keep the reconciler's
-- sweep cheap even with many historical rows.

ALTER TABLE scenarios       ADD COLUMN IF NOT EXISTS supermemory_id TEXT;
ALTER TABLE decision_traces ADD COLUMN IF NOT EXISTS supermemory_id TEXT;

CREATE INDEX IF NOT EXISTS idx_scenarios_pending_sm
    ON scenarios(installation_id)
    WHERE supermemory_id IS NULL AND active = TRUE;

CREATE INDEX IF NOT EXISTS idx_decision_traces_pending_sm
    ON decision_traces(repo_id)
    WHERE supermemory_id IS NULL;
