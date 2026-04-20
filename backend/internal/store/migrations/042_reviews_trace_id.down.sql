DROP INDEX IF EXISTS reviews_trace_id_idx;
ALTER TABLE reviews DROP COLUMN IF EXISTS trace_id;
