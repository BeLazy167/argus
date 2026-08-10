DROP INDEX IF EXISTS repos_graph_index_due;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_indexed_at;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_index_attempted_at;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_index_cursor;
