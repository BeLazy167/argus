-- PostgreSQL is the authority for review-post reconciliation age. Marker text
-- remains useful for operators, but clocks on application machines never authorize
-- clearing a claim after a negative GitHub lookup.
ALTER TABLE reviews
    ADD COLUMN review_post_claimed_at TIMESTAMPTZ;
