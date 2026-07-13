-- Revert to migration 050's definition: COALESCE without NULLIF (empty
-- change_class leaks into a phantom '' bucket).
--
-- The up migration's llm-pending backfill is intentionally NOT reverted: it is
-- a data fix applying the semantics every consumer already assumed (empty
-- change_class == production), and restoring empty strings would recreate the
-- bug it fixed. Backfilled rows stay change_class='production',
-- source='llm-default' with the 'intent:unresolved' signal.
CREATE OR REPLACE VIEW vw_review_gauge AS
SELECT
    rp.installation_id,
    COALESCE(rc.category, 'uncategorized')                       AS category,
    COALESCE(r.review_contract->>'change_class', 'production')   AS change_class,
    COUNT(DISTINCT rc.id)                                        AS posted_findings,
    COUNT(DISTINCT rc.id) FILTER (WHERE co.outcome = 'addressed_human') AS addressed_human,
    COUNT(DISTINCT rc.id) FILTER (WHERE co.outcome = 'addressed_agent') AS addressed_agent,
    COUNT(DISTINCT rc.id) FILTER (WHERE co.outcome = 'dismissed')       AS dismissed,
    COUNT(DISTINCT rc.id) FILTER (WHERE co.outcome = 'ignored')         AS ignored,
    COUNT(DISTINCT rc.id) FILTER (WHERE co.outcome = 'deferred')        AS deferred,
    ROUND(
        (COUNT(DISTINCT rc.id) FILTER (WHERE co.outcome = 'addressed_human')
         + 0.5 * COUNT(DISTINCT rc.id) FILTER (WHERE co.outcome = 'addressed_agent'))::numeric
        / NULLIF(COUNT(DISTINCT rc.id), 0),
        3
    )                                                            AS address_rate,
    ROUND(
        COUNT(DISTINCT rc.id) FILTER (WHERE co.outcome = 'dismissed')::numeric
        / NULLIF(COUNT(DISTINCT rc.id), 0),
        3
    )                                                            AS dismiss_rate,
    PERCENTILE_CONT(0.5) WITHIN GROUP (
        ORDER BY EXTRACT(EPOCH FROM (co.addressed_at - rc.created_at))
    ) FILTER (WHERE co.addressed_at IS NOT NULL)                 AS median_seconds_to_merge
FROM review_comments rc
JOIN reviews r ON r.id = rc.review_id
JOIN repos rp ON rp.id = r.repo_id
LEFT JOIN comment_outcomes co ON co.review_comment_id = rc.id
WHERE rc.suppressed_reason IS NULL
  AND rc.github_comment_id IS NOT NULL
GROUP BY rp.installation_id, COALESCE(rc.category, 'uncategorized'),
         COALESCE(r.review_contract->>'change_class', 'production');
