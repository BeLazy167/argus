-- name: UpsertCodeNode :one
INSERT INTO code_nodes (repo_id, kind, name, file_path, line_start, line_end, language, pr_number, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
ON CONFLICT (repo_id, file_path, kind, name)
DO UPDATE SET line_start = $5, line_end = $6, language = $7, pr_number = $8, updated_at = NOW()
RETURNING id;

-- name: UpsertCodeEdge :exec
INSERT INTO code_edges (repo_id, source_id, target_id, kind, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (repo_id, source_id, target_id, kind) DO NOTHING;

-- name: DeleteNodesByFile :exec
DELETE FROM code_nodes WHERE repo_id = $1 AND file_path = $2;

-- name: ListGraphNodes :many
SELECT id, repo_id, kind, name, file_path, line_start, line_end, language, pr_number, is_merged
FROM code_nodes WHERE repo_id = $1 ORDER BY file_path, name;

-- name: ListGraphEdges :many
SELECT ce.id, ce.repo_id, ce.source_id, ce.target_id, ce.kind,
       sn.name as source_name, tn.name as target_name
FROM code_edges ce
JOIN code_nodes sn ON sn.id = ce.source_id
JOIN code_nodes tn ON tn.id = ce.target_id
WHERE ce.repo_id = $1;

-- name: MarkNodesMerged :exec
UPDATE code_nodes SET is_merged = true WHERE repo_id = $1 AND pr_number = $2;

-- name: DeleteUnmergedNodesByPR :exec
DELETE FROM code_nodes WHERE repo_id = $1 AND pr_number = $2 AND is_merged = false;

-- name: ListArchNodes :many
-- Returns all code nodes for a repo, used to compute file-level architecture metrics.
SELECT file_path, COALESCE(language, '')::text as language, name,
       (COALESCE(line_end, 0) - COALESCE(line_start, 0) + 1)::int as line_span
FROM code_nodes
WHERE repo_id = $1
ORDER BY file_path, line_start;

-- name: ListArchFileEdges :many
-- Returns inter-file edges (excludes self-references) for fan-in/fan-out + edge graph.
SELECT src.file_path as source_path, tgt.file_path as target_path, ce.kind
FROM code_edges ce
JOIN code_nodes src ON src.id = ce.source_id
JOIN code_nodes tgt ON tgt.id = ce.target_id
WHERE ce.repo_id = $1 AND src.file_path != tgt.file_path;

-- name: ListArchBugDensity :many
-- Returns bug count + PR count per file for bug density and change frequency metrics.
-- Bugs are deduped by (pr_number, end_line) so a single defect reported many
-- times in one PR counts once — a noisy PR no longer inflates density.
SELECT rc.file_path,
       COUNT(DISTINCT CONCAT(r.pr_number::text, ':', COALESCE(rc.end_line::text, '0')))
           FILTER (WHERE rc.severity IN ('critical','warning'))::int AS bugs,
       COUNT(DISTINCT r.pr_number)::int AS prs
FROM review_comments rc
JOIN reviews r ON r.id = rc.review_id
WHERE r.repo_id = $1
GROUP BY rc.file_path;

-- name: ListArchCoupling :many
-- Returns last 200 PRs with their touched files for Jaccard co-change coupling.
SELECT r.pr_number, array_agg(DISTINCT rc.file_path)::text[] AS files
FROM review_comments rc
JOIN reviews r ON r.id = rc.review_id
WHERE r.repo_id = $1 AND r.status = 'completed'
GROUP BY r.pr_number
ORDER BY r.pr_number DESC
LIMIT 200;

-- name: GetTopChokePoints :many
-- Top files by fan_in (used for review prompt context injection + memory indexing).
SELECT tgt.file_path, COUNT(DISTINCT src.file_path)::int as fan_in
FROM code_edges ce
JOIN code_nodes src ON src.id = ce.source_id
JOIN code_nodes tgt ON tgt.id = ce.target_id
WHERE ce.repo_id = $1 AND src.file_path != tgt.file_path
GROUP BY tgt.file_path
ORDER BY fan_in DESC
LIMIT $2;

-- name: GetFileFanIn :one
-- Single-file fan-in lookup for review prompt enrichment.
SELECT COUNT(DISTINCT src.file_path)::int as fan_in
FROM code_edges ce
JOIN code_nodes src ON src.id = ce.source_id
JOIN code_nodes tgt ON tgt.id = ce.target_id
WHERE ce.repo_id = $1 AND tgt.file_path = $2 AND src.file_path != tgt.file_path;

-- name: GetFileBugCount :one
-- Single-file bug count for review prompt enrichment.
SELECT COUNT(*)::int as bugs
FROM review_comments rc
JOIN reviews r ON r.id = rc.review_id
WHERE r.repo_id = $1 AND rc.file_path = $2 AND rc.severity IN ('critical','warning');

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
