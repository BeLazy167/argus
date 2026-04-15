-- Revert: remove 'cancelled' from allowed statuses.
-- First update any cancelled reviews to 'failed' so the constraint doesn't fail.
UPDATE reviews SET status = 'failed' WHERE status = 'cancelled';

ALTER TABLE reviews DROP CONSTRAINT IF EXISTS reviews_status_check;
ALTER TABLE reviews ADD CONSTRAINT reviews_status_check
  CHECK (status IN ('pending','in_progress','completed','failed'));
