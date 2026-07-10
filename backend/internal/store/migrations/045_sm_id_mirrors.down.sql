DROP INDEX IF EXISTS idx_decision_traces_pending_sm;
DROP INDEX IF EXISTS idx_scenarios_pending_sm;
ALTER TABLE decision_traces DROP COLUMN IF EXISTS supermemory_id;
ALTER TABLE scenarios       DROP COLUMN IF EXISTS supermemory_id;
