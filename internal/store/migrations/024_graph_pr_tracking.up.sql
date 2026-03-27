ALTER TABLE code_nodes ADD COLUMN IF NOT EXISTS pr_number integer;
ALTER TABLE code_nodes ADD COLUMN IF NOT EXISTS is_merged boolean NOT NULL DEFAULT false;
