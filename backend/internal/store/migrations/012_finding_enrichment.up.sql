ALTER TABLE review_comments ADD COLUMN IF NOT EXISTS matched_pattern_id BIGINT REFERENCES patterns(id);
ALTER TABLE review_comments ADD COLUMN IF NOT EXISTS matched_pattern_score REAL;
ALTER TABLE review_comments ADD COLUMN IF NOT EXISTS enforced_rule_content TEXT;
ALTER TABLE review_comments ADD COLUMN IF NOT EXISTS is_new_finding BOOLEAN DEFAULT false;
