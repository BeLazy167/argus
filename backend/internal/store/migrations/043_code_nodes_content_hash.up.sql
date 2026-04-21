-- Adds content_hash column so graph.IndexFiles can skip no-op upserts.
-- Nullable: existing rows get NULL on migration, repopulate on first
-- re-index. No dedicated index — the column is read as part of per-file
-- SELECTs already covered by idx_code_nodes_file (repo_id, file_path).
ALTER TABLE code_nodes ADD COLUMN content_hash TEXT;
