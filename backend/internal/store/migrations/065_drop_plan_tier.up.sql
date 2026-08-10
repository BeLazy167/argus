-- The Pro tier is gone: Argus is open source and self-deployable, and no code
-- reads this column. It was already inert -- GetPlanTier and SetPlanTier had
-- no callers, and the gating that once consulted them was removed earlier.
ALTER TABLE installations DROP COLUMN IF EXISTS plan_tier;
