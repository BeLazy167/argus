CREATE TABLE comment_outcomes (
  id SERIAL PRIMARY KEY,
  review_comment_id UUID NOT NULL REFERENCES review_comments(id),
  outcome TEXT NOT NULL CHECK (outcome IN ('confirmed','dismissed','ignored')),
  created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_comment_outcomes_review ON comment_outcomes(review_comment_id);
