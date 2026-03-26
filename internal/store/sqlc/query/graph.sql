-- name: UpsertCodeNode :one
INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
ON CONFLICT (repo_id, file_path, kind, name)
DO UPDATE SET line_start = $5, line_end = $6, language = $7, updated_at = NOW()
RETURNING id;

-- name: UpsertCodeEdge :exec
INSERT INTO code_edges (repo_id, source_id, target_id, kind, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (repo_id, source_id, target_id, kind) DO NOTHING;

-- name: DeleteNodesByFile :exec
DELETE FROM code_nodes WHERE repo_id = $1 AND file_path = $2;

-- name: ListGraphNodes :many
SELECT id, repo_id, kind, name, file_path, line_start, line_end, language
FROM code_nodes WHERE repo_id = $1 ORDER BY file_path, name;

-- name: ListGraphEdges :many
SELECT ce.id, ce.repo_id, ce.source_id, ce.target_id, ce.kind,
       sn.name as source_name, tn.name as target_name
FROM code_edges ce
JOIN code_nodes sn ON sn.id = ce.source_id
JOIN code_nodes tn ON tn.id = ce.target_id
WHERE ce.repo_id = $1;

-- name: GetBlastRadius :many
WITH RECURSIVE affected AS (
    SELECT id, name, file_path, kind, 0 as depth
    FROM code_nodes WHERE code_nodes.repo_id = sqlc.arg(repo_id) AND code_nodes.file_path = ANY(sqlc.arg(file_paths)::text[])
    UNION
    SELECT cn.id, cn.name, cn.file_path, cn.kind, a.depth + 1
    FROM code_nodes cn
    JOIN code_edges ce ON ce.source_id = cn.id
    JOIN affected a ON ce.target_id = a.id
    WHERE a.depth < sqlc.arg(max_depth)::int AND cn.repo_id = sqlc.arg(repo_id)
)
SELECT DISTINCT id, name, file_path, kind, depth FROM affected ORDER BY depth, file_path LIMIT 50;
