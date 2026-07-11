-- Gauge, part 2: address-rate telemetry per category per change_class.
--
-- address_rate is human-weighted: a finding fixed by a human counts 1.0, a
-- finding fixed by a bot/agent counts 0.5 (agents fix whatever they're told —
-- human follow-through is the stronger signal that the finding mattered).
-- Suppressed findings were never posted, so — like vw_memory_ab — they are
-- excluded entirely; so are rows whose comment never reached GitHub
-- (github_comment_id IS NULL, e.g. post failed).
--
-- median_seconds_to_merge: posted-comment → addressed-at-merge latency for
-- findings the detection job marked addressed.
--
-- installation_id is included so the internal API can scope rows to the
-- caller's installations (dashboard use).
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
