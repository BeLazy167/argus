ALTER TABLE code_nodes ADD COLUMN IF NOT EXISTS pr_number integer;
ALTER TABLE code_nodes ADD COLUMN IF NOT EXISTS is_merged boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_code_nodes_repo_pr ON code_nodes (repo_id, pr_number) WHERE pr_number IS NOT NULL;
