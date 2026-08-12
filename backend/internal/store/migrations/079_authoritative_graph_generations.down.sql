ALTER TABLE repos DROP COLUMN IF EXISTS graph_index_tree_truncated;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_index_skipped_files;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_index_failed_files;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_index_visited_files;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_index_expected_files;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_index_commit_sha;
ALTER TABLE repos DROP COLUMN IF EXISTS graph_published_generation_id;
DROP TABLE IF EXISTS graph_index_generation_files;
DROP TABLE IF EXISTS graph_index_generations;
