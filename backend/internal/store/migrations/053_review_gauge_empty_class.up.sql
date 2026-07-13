-- Gauge fix: bucket empty change_class as production.
--
-- Migration 050's COALESCE(review_contract->>'change_class', 'production') only
-- catches a NULL/absent key, not the empty string that a contract left
-- Source="llm-pending" persists (intent stage fast-exit). Those rows leaked into
-- a phantom '' bucket instead of 'production'. NULLIF folds '' into the COALESCE
-- default so empty and absent both bucket as production, matching the
-- treat-empty-as-production behavior everywhere else in the pipeline.
CREATE OR REPLACE VIEW vw_review_gauge AS
SELECT
    rp.installation_id,
    COALESCE(rc.category, 'uncategorized')                                    AS category,
    COALESCE(NULLIF(r.review_contract->>'change_class', ''), 'production')    AS change_class,
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
         COALESCE(NULLIF(r.review_contract->>'change_class', ''), 'production');

-- Backfill: reviews persisted before ReviewContract.Finalize existed still
-- carry source='llm-pending' with an empty change_class and render Class "—"
-- on the review detail page forever. Apply Finalize's exact semantics to the
-- stored JSONB: change_class='production', source='llm-default', and append
-- the 'intent:unresolved' signal. The signals key has THREE historical shapes:
-- absent (post-#144 omitempty ⇒ SQL NULL, caught by COALESCE), JSON null
-- (pre-#144 marshalled nil without omitempty — `||` would splice a null
-- ELEMENT into the array, so NULLIF folds it first), and a real array. The
-- change_class='' guard keeps the backfill provably non-lossy if an anomalous
-- row ever carried a pending source with a resolved class. Safe: nothing reads
-- the persisted source back into the pipeline, and retry paths recompute
-- contracts fresh. Intentionally irreversible — see the down migration.
UPDATE reviews
SET review_contract = jsonb_set(
        jsonb_set(
            jsonb_set(review_contract, '{change_class}', '"production"'),
            '{source}', '"llm-default"'
        ),
        '{signals}',
        COALESCE(NULLIF(review_contract->'signals', 'null'::jsonb), '[]'::jsonb)
            || '["intent:unresolved"]'::jsonb
    )
WHERE review_contract->>'source' = 'llm-pending'
  AND review_contract->>'change_class' = '';
