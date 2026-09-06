-- name: ListPatterns :many
SELECT p.id, p.installation_id, p.repo_id, p.content, p.memory_doc_id, p.created_by, COALESCE(p.source, 'manual') as source, p.category, p.pr_number, p.created_at, p.updated_at,
CASE WHEN m.id IS NULL THEN 'unmirrored' WHEN m.deleted_at IS NOT NULL THEN 'deleted' WHEN m.invalidated_at IS NOT NULL OR m.superseded_by IS NOT NULL THEN 'superseded' WHEN EXISTS (SELECT 1 FROM convention_conflicts c WHERE c.state='open' AND (c.memory_low_id=m.id OR c.memory_high_id=m.id)) THEN 'disputed' ELSE 'active' END::text AS status,
COALESCE((SELECT count(*) FROM convention_evidence e WHERE e.convention_memory_id=m.id), 1)::int AS evidence_count
FROM patterns p LEFT JOIN memories m ON m.installation_id=p.installation_id AND m.custom_id=p.memory_custom_id WHERE p.installation_id = ANY($1::bigint[]) ORDER BY p.created_at DESC;

-- name: ListPatternsForRepo :many
SELECT p.id, p.installation_id, p.repo_id, p.content, p.memory_doc_id, p.created_by, COALESCE(p.source, 'manual') as source, p.category, p.pr_number, p.created_at, p.updated_at,
CASE WHEN m.id IS NULL THEN 'unmirrored' WHEN m.deleted_at IS NOT NULL THEN 'deleted' WHEN m.invalidated_at IS NOT NULL OR m.superseded_by IS NOT NULL THEN 'superseded' WHEN EXISTS (SELECT 1 FROM convention_conflicts c WHERE c.state='open' AND (c.memory_low_id=m.id OR c.memory_high_id=m.id)) THEN 'disputed' ELSE 'active' END::text AS status,
COALESCE((SELECT count(*) FROM convention_evidence e WHERE e.convention_memory_id=m.id), 1)::int AS evidence_count
FROM patterns p LEFT JOIN memories m ON m.installation_id=p.installation_id AND m.custom_id=p.memory_custom_id WHERE p.installation_id = ANY($1::bigint[]) AND (p.repo_id IS NULL OR p.repo_id = $2) ORDER BY p.created_at DESC;

-- name: CreatePattern :one
INSERT INTO patterns (installation_id, repo_id, content, memory_doc_id, created_by, source, category, pr_number, memory_custom_id)
VALUES ($1, $2, $3, $4, $5, COALESCE(sqlc.narg(source)::text, 'manual'), $6, $7, sqlc.narg(memory_custom_id)::text)
ON CONFLICT (installation_id, memory_custom_id) WHERE memory_custom_id IS NOT NULL DO UPDATE
SET repo_id = EXCLUDED.repo_id,
    content = EXCLUDED.content,
    memory_doc_id = COALESCE(EXCLUDED.memory_doc_id, patterns.memory_doc_id),
    created_by = COALESCE(EXCLUDED.created_by, patterns.created_by),
    source = EXCLUDED.source,
    category = EXCLUDED.category,
    pr_number = EXCLUDED.pr_number,
    updated_at = now()
RETURNING id, installation_id, repo_id, content, memory_doc_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at;

-- name: GetPattern :one
SELECT id, installation_id, repo_id, content, memory_doc_id, memory_custom_id, created_by, COALESCE(source, 'manual') as source, category, pr_number, created_at, updated_at
FROM patterns WHERE id = sqlc.arg(id)::bigint;

-- name: DeletePattern :one
DELETE FROM patterns
WHERE id = sqlc.arg(id)::bigint AND installation_id = ANY(sqlc.arg(installation_ids)::bigint[])
RETURNING installation_id, memory_custom_id, memory_doc_id,
          repo_id, content, COALESCE(source, 'manual')::text AS source, category, pr_number;

-- name: GetPatternStats :many
SELECT DATE_TRUNC('week', created_at)::timestamptz AS week, COALESCE(source, 'manual') as source, COUNT(*)::int as count
FROM patterns WHERE installation_id = ANY($1::bigint[])
GROUP BY week, source ORDER BY week;

-- name: GetLowQualityPatterns :many
SELECT id, installation_id, repo_id, memory_doc_id, content_hash, category,
       times_matched, times_confirmed, times_dismissed, quality_score,
       last_matched_at, created_at, updated_at
FROM pattern_stats
WHERE installation_id = $1 AND quality_score <= $2
ORDER BY quality_score ASC
LIMIT sqlc.arg(row_limit)::bigint;
