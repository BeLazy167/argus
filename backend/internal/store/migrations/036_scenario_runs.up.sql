-- Scenario runs: one row per (scenario, review) simulation outcome.
-- Powers the per-scenario history timeline on /scenarios and any drift analytics.
-- root_cause + impact are kept alongside why/fix so the export API can still surface
-- the full LLM output even though the rendered GitHub block only uses why/fix.
CREATE TABLE scenario_runs (
    id           BIGSERIAL PRIMARY KEY,
    scenario_id  BIGINT NOT NULL REFERENCES scenarios(id) ON DELETE CASCADE,
    review_id    UUID   NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    pr_number    INT    NOT NULL,
    verdict      TEXT   NOT NULL CHECK (verdict IN ('broken','fixed','partial','unclear')),
    confidence   NUMERIC(4,3) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    why          TEXT,
    fix          TEXT,
    root_cause   TEXT,
    impact       TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (scenario_id, review_id)
);
CREATE INDEX idx_scenario_runs_scenario ON scenario_runs(scenario_id, created_at DESC);
CREATE INDEX idx_scenario_runs_review   ON scenario_runs(review_id);

-- Denormalized "last run" summary on scenarios so the list page is a single-table scan
-- without a per-row JOIN. last_run_at already exists (migration 018).
ALTER TABLE scenarios
    ADD COLUMN last_verdict    TEXT CHECK (last_verdict IN ('broken','fixed','partial','unclear')),
    ADD COLUMN last_confidence NUMERIC(4,3) CHECK (last_confidence IS NULL OR (last_confidence >= 0 AND last_confidence <= 1)),
    ADD COLUMN last_why        TEXT,
    ADD COLUMN last_fix        TEXT,
    ADD COLUMN last_pr_number  INT,
    ADD COLUMN last_review_id  UUID REFERENCES reviews(id) ON DELETE SET NULL;
