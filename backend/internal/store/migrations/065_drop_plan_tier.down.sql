-- Restores the column and its historical default. Values are not restored;
-- every installation reads as 'free', which is what the product now offers.
ALTER TABLE installations ADD COLUMN IF NOT EXISTS plan_tier TEXT NOT NULL DEFAULT 'free';
