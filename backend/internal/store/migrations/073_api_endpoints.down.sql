-- Inferred edges must go BEFORE the constraint is narrowed, or the ADD
-- CONSTRAINT below fails validation against rows the old constraint forbids.
DELETE FROM code_edges WHERE kind = 'calls_api';

ALTER TABLE code_edges DROP CONSTRAINT IF EXISTS code_edges_kind_check;
ALTER TABLE code_edges ADD CONSTRAINT code_edges_kind_check
    CHECK (kind IN ('calls','imports','inherits','implements','uses_type'));

ALTER TABLE code_edges DROP COLUMN IF EXISTS inferred;

DROP TABLE IF EXISTS api_endpoints;
