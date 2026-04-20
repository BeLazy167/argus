ALTER TABLE reviews ADD COLUMN trace_id VARCHAR(36);
CREATE INDEX reviews_trace_id_idx ON reviews(trace_id) WHERE trace_id IS NOT NULL;
