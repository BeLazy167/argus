-- Add 'cancelled' as a valid review status for the stop-review feature.
ALTER TABLE reviews DROP CONSTRAINT IF EXISTS reviews_status_check;
ALTER TABLE reviews ADD CONSTRAINT reviews_status_check
  CHECK (status IN ('pending','in_progress','completed','failed','cancelled'));

-- Also exclude cancelled from the RecoverIncomplete query (pipeline_states).
-- No schema change needed — the SM query already filters by state name.
