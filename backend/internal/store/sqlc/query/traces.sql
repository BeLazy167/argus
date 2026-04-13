-- name: CreateTrace :exec
INSERT INTO decision_traces (repo_id, file_path, symbol_name, trace_type, content, severity, review_id, pr_number, metadata)
VALUES ($1, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''), $7, $8, $9);

-- name: ListTracesForFiles :many
SELECT id, repo_id, file_path, COALESCE(symbol_name, '') as symbol_name, trace_type, content, COALESCE(severity, '') as severity, review_id, COALESCE(pr_number, 0) as pr_number, COALESCE(metadata, '{}') as metadata, created_at
FROM decision_traces
WHERE repo_id = $1 AND file_path = ANY($2::text[])
ORDER BY created_at DESC
LIMIT $3;

-- name: ListTracesForRepo :many
SELECT id, repo_id, file_path, COALESCE(symbol_name, '') as symbol_name, trace_type, content, COALESCE(severity, '') as severity, review_id, COALESCE(pr_number, 0) as pr_number, COALESCE(metadata, '{}') as metadata, created_at
FROM decision_traces
WHERE repo_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: GetFileRiskScore :one
SELECT COALESCE(SUM(
    CASE severity
        WHEN 'critical' THEN 5
        WHEN 'warning' THEN 3
        WHEN 'suggestion' THEN 1
        ELSE 1
    END
), 0)::int as risk_score
FROM decision_traces
WHERE repo_id = $1 AND file_path = $2 AND created_at > NOW() - INTERVAL '90 days';

-- name: GetHotFiles :many
SELECT file_path, COUNT(*)::int AS trace_count, MAX(created_at) AS last_trace
FROM decision_traces
WHERE repo_id = $1 AND created_at > NOW() - INTERVAL '90 days'
GROUP BY file_path
ORDER BY trace_count DESC
LIMIT $2;
