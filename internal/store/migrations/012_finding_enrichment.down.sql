ALTER TABLE review_comments DROP COLUMN IF EXISTS matched_pattern_id;
ALTER TABLE review_comments DROP COLUMN IF EXISTS matched_pattern_score;
ALTER TABLE review_comments DROP COLUMN IF EXISTS enforced_rule_content;
ALTER TABLE review_comments DROP COLUMN IF EXISTS is_new_finding;
