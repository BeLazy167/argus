-- name: UpsertCodeNode :one
-- installation_id is derived from repos rather than taken as a parameter so the
-- denormalised tenant column can never disagree with repos.installation_id.
-- See store/graph.go installationOfRepo.
INSERT INTO code_nodes (repo_id, installation_id, kind, name, file_path, line_start, line_end, language, pr_number, updated_at)
VALUES ($1, (SELECT r.installation_id FROM repos r WHERE r.id = $1), $2, $3, $4, $5, $6, $7, $8, NOW())
ON CONFLICT (repo_id, file_path, kind, name)
DO UPDATE SET line_start = $5, line_end = $6, language = $7, pr_number = $8,
              installation_id = EXCLUDED.installation_id, updated_at = NOW()
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
-- `NOT ce.inferred` excludes derived cross-repo API edges, and it is load
-- bearing rather than tidy. Those edges carry the CLIENT's repo_id while their
-- target node lives in another repository, so this query would return an edge
-- pointing at a node id that ListGraphNodes — filtered on the same repo_id —
-- does not contain. The canvas would render a dangling edge to nothing.
SELECT ce.id, ce.repo_id, ce.source_id, ce.target_id, ce.kind,
       sn.name as source_name, tn.name as target_name
FROM code_edges ce
JOIN code_nodes sn ON sn.id = ce.source_id
JOIN code_nodes tn ON tn.id = ce.target_id
WHERE ce.repo_id = $1 AND NOT ce.inferred;

-- name: MarkNodesMerged :exec
UPDATE code_nodes SET is_merged = true WHERE repo_id = $1 AND pr_number = $2;

-- name: DeleteUnmergedNodesByPR :exec
DELETE FROM code_nodes WHERE repo_id = $1 AND pr_number = $2 AND is_merged = false;

-- name: ListArchNodes :many
-- Returns published code nodes for a repo, used to compute file-level architecture metrics.
-- The generation pointer is the publication authority for the physical projection.
-- Repositories predating authoritative generations may still have legacy rows; those
-- rows must not become API topology merely because they were deliberately retained.
SELECT cn.file_path, COALESCE(cn.language, '')::text AS language, cn.name, cn.kind,
       COALESCE(cn.line_start, 0)::int AS line_start,
       COALESCE(cn.line_end, 0)::int AS line_end
FROM code_nodes cn
JOIN repos authority ON authority.id = cn.repo_id
JOIN graph_index_generations published
  ON published.id = authority.graph_published_generation_id
 AND published.repo_id = authority.id
WHERE cn.repo_id = $1
ORDER BY cn.file_path, cn.line_start;

-- name: ListArchFileEdges :many
-- Returns inter-file edges (excludes self-references) for fan-in/fan-out + edge graph.
--
-- `NOT ce.inferred`: a derived cross-repo API edge names a file in ANOTHER
-- repository, and every metric built on this query — fan-in, fan-out, coupling,
-- choke points — is a statement about THIS repository's architecture. Letting
-- one in adds a foreign file to the file set and inflates the counts.
SELECT src.file_path as source_path, tgt.file_path as target_path, ce.kind
FROM code_edges ce
JOIN repos authority ON authority.id = ce.repo_id
JOIN graph_index_generations published
  ON published.id = authority.graph_published_generation_id
 AND published.repo_id = authority.id
JOIN code_nodes src ON src.id = ce.source_id
JOIN code_nodes tgt ON tgt.id = ce.target_id
WHERE ce.repo_id = $1 AND src.file_path != tgt.file_path AND NOT ce.inferred;

-- name: ListArchBugDensity :many
-- Returns bug count + PR count per file for bug density and change frequency metrics.
-- Bugs are deduped by (pr_number, end_line) so a single defect reported many
-- times in one PR counts once — a noisy PR no longer inflates density.
--
-- `state <> 'suppressed'` sits in the bugs FILTER, not in the WHERE, and the
-- placement is the whole point:
--   * bugs is a defect claim. A suppressed finding was generated and then
--     withheld — the PR author never saw it. Counting it produced nonzero
--     bug_density, a raised risk score and the "Bug hotspot. High defect rate
--     per line." label for files whose findings were ALL suppressed (#239).
--   * prs is change frequency, not a defect claim. A PR whose only findings on
--     a file were suppressed still changed that file, so it must keep counting;
--     moving the predicate to the WHERE would undercount churn and drop
--     all-suppressed files from the result set entirely.
-- Predicate matches ListPRReviewSummaries (queries.go) exactly — one spelling
-- of "was this finding actually delivered" across the codebase.
SELECT rc.file_path,
       COUNT(DISTINCT CONCAT(r.pr_number::text, ':', COALESCE(rc.end_line::text, '0')))
           FILTER (WHERE rc.severity IN ('critical','warning') AND rc.state <> 'suppressed')::int AS bugs,
       COUNT(DISTINCT r.pr_number)::int AS prs
FROM review_comments rc
JOIN reviews r ON r.id = rc.review_id
WHERE r.repo_id = $1
  AND rc.attempt_generation = r.attempt_generation
GROUP BY rc.file_path;

-- name: ListArchCoupling :many
-- Returns actual changed files from the latest persisted pipeline state of the
-- last 200 completed reviews. Findings are not a file-change ledger: clean files
-- have no review_comment row and must still contribute to co-change metrics.
WITH recent_reviews AS (
    SELECT id, pr_number
    FROM reviews
    WHERE repo_id = $1 AND status = 'completed'
    ORDER BY created_at DESC
    LIMIT 200
), changed AS (
    SELECT r.pr_number,
           COALESCE(NULLIF(f->>'NewName', '/dev/null'), NULLIF(f->>'new_name', '/dev/null'),
                    NULLIF(f->>'OldName', '/dev/null'), NULLIF(f->>'old_name', '/dev/null')) AS file_path
    FROM recent_reviews r
    CROSS JOIN LATERAL (
        SELECT payload
        FROM pipeline_states
        WHERE review_id = r.id
        ORDER BY updated_at DESC
        LIMIT 1
    ) ps
    CROSS JOIN LATERAL jsonb_array_elements(
        COALESCE(ps.payload->'Diff'->'Files', ps.payload->'diff'->'files', '[]'::jsonb)
    ) AS f
)
SELECT pr_number, array_agg(DISTINCT file_path)::text[] AS files
FROM changed
WHERE file_path IS NOT NULL AND file_path <> ''
GROUP BY pr_number
ORDER BY pr_number DESC;

-- name: GetTopChokePoints :many
-- Top files by fan_in (used for review prompt context injection + memory indexing).
-- `NOT ce.inferred` for the same reason as ListArchFileEdges: this ranks files
-- WITHIN one repository, and a cross-repo edge would list a foreign file.
SELECT tgt.file_path, COUNT(DISTINCT src.file_path)::int as fan_in
FROM code_edges ce
JOIN code_nodes src ON src.id = ce.source_id
JOIN code_nodes tgt ON tgt.id = ce.target_id
WHERE ce.repo_id = $1 AND src.file_path != tgt.file_path AND NOT ce.inferred
GROUP BY tgt.file_path
ORDER BY fan_in DESC
LIMIT $2;

-- name: GetFileFanIn :one
-- Single-file fan-in lookup for review prompt enrichment. `NOT ce.inferred`
-- keeps the number the review LLM is told a count of parsed dependents.
SELECT COUNT(DISTINCT src.file_path)::int as fan_in
FROM code_edges ce
JOIN code_nodes src ON src.id = ce.source_id
JOIN code_nodes tgt ON tgt.id = ce.target_id
WHERE ce.repo_id = $1 AND tgt.file_path = $2 AND src.file_path != tgt.file_path
  AND NOT ce.inferred;

-- name: GetFileBugCount :one
-- Single-file bug count for review prompt enrichment. Same predicate as
-- ListArchBugDensity's bugs FILTER: a suppressed finding never reached the PR,
-- so counting it would tell the review LLM "N bugs have been found in this
-- file" about defects nobody was ever shown.
SELECT COUNT(*)::int as bugs
FROM review_comments rc
JOIN reviews r ON r.id = rc.review_id
WHERE r.repo_id = $1 AND rc.file_path = $2 AND rc.severity IN ('critical','warning')
  AND rc.attempt_generation = r.attempt_generation
  AND rc.state <> 'suppressed';

-- name: GetFileMemoryPatterns :many
SELECT DISTINCT p.id, p.installation_id, p.repo_id, p.content, p.memory_doc_id,
       p.created_by, COALESCE(p.source, 'manual') AS source, p.category, p.pr_number,
       p.created_at, p.updated_at
FROM patterns p
JOIN review_comments rc ON rc.matched_pattern_id = p.id
JOIN reviews r ON r.id = rc.review_id
WHERE rc.file_path = $1 AND r.repo_id = $2
  AND rc.attempt_generation = r.attempt_generation
ORDER BY p.created_at DESC
LIMIT 10;

-- name: GetFileMemoryComments :many
SELECT rc.id, rc.review_id, rc.file_path, rc.start_line, rc.end_line, rc.side,
       rc.body, rc.severity, rc.category, rc.specialist, rc.confidence_score,
       rc.code_snippet, rc.github_comment_id, rc.matched_pattern_id,
       rc.matched_pattern_score, rc.enforced_rule_content, rc.is_new_finding, rc.created_at,
       rc.state, rc.suppressed_reason, rc.resolved_sha, rc.attempt_generation
FROM review_comments rc
JOIN reviews r ON r.id = rc.review_id
WHERE rc.file_path = $1 AND r.repo_id = $2
  AND rc.attempt_generation = r.attempt_generation
ORDER BY (rc.state = 'suppressed'), rc.created_at DESC
LIMIT 5;
