-- Persist the fleet-wide full-index cadence. The singleton survives process
-- restarts and makes every machine share one conservative GitHub API budget.
CREATE TABLE graph_index_budget (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    last_window_started_at TIMESTAMPTZ
);
INSERT INTO graph_index_budget (singleton) VALUES (TRUE);
