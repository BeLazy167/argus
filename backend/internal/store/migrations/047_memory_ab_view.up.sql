-- Memory effectiveness A/B: acceptance rate of posted findings on reviews that
-- ran with memory vs without. Operator/read-only view — the app does not query
-- it; PostHog events (memory.enriched / memory.pattern_feedback) carry the
-- same join for dashboards. Suppressed comments are tallied separately: they
-- were never posted, so they must not dilute acceptance_rate.
CREATE OR REPLACE VIEW vw_memory_ab AS
SELECT
    r.memory_enabled,
    COUNT(DISTINCT r.id)                                                  AS reviews,
    COUNT(rc.id) FILTER (WHERE rc.suppressed_reason IS NULL)              AS posted_comments,
    COUNT(rc.id) FILTER (WHERE rc.suppressed_reason IS NOT NULL)          AS suppressed_comments,
    COUNT(co.review_comment_id) FILTER (WHERE co.outcome = 'confirmed')   AS confirmed,
    COUNT(co.review_comment_id) FILTER (WHERE co.outcome = 'dismissed')   AS dismissed,
    ROUND(
        COUNT(co.review_comment_id) FILTER (WHERE co.outcome = 'confirmed')::numeric
        / NULLIF(COUNT(co.review_comment_id) FILTER (WHERE co.outcome IN ('confirmed', 'dismissed')), 0),
        3
    )                                                                     AS acceptance_rate
FROM reviews r
LEFT JOIN review_comments rc ON rc.review_id = r.id
LEFT JOIN comment_outcomes co ON co.review_comment_id = rc.id
GROUP BY r.memory_enabled;
