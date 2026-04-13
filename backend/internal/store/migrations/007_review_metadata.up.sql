-- Add review metadata columns for dashboard visibility
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS deep_review BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS persona TEXT;
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS is_incremental BOOLEAN NOT NULL DEFAULT FALSE;

-- Add specialist and confidence score to review comments
ALTER TABLE review_comments ADD COLUMN IF NOT EXISTS specialist TEXT;
ALTER TABLE review_comments ADD COLUMN IF NOT EXISTS confidence_score INT;

-- Allow 'scoring' as a valid model_configs stage
ALTER TABLE model_configs DROP CONSTRAINT IF EXISTS model_configs_stage_check;
ALTER TABLE model_configs ADD CONSTRAINT model_configs_stage_check
    CHECK (stage IN ('triage','review','synthesis','embedding','scoring'));
