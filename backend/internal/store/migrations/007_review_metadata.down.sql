ALTER TABLE reviews DROP COLUMN IF EXISTS deep_review;
ALTER TABLE reviews DROP COLUMN IF EXISTS persona;
ALTER TABLE reviews DROP COLUMN IF EXISTS is_incremental;

ALTER TABLE review_comments DROP COLUMN IF EXISTS specialist;
ALTER TABLE review_comments DROP COLUMN IF EXISTS confidence_score;

ALTER TABLE model_configs DROP CONSTRAINT IF EXISTS model_configs_stage_check;
ALTER TABLE model_configs ADD CONSTRAINT model_configs_stage_check
    CHECK (stage IN ('triage','review','synthesis','embedding'));
