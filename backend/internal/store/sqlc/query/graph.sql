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
-- Returns all code nodes for a repo, used to compute file-level architecture metrics.
SELECT file_path, COALESCE(language, '')::text as language, name,
       (COALESCE(line_end, 0) - COALESCE(line_start, 0) + 1)::int as line_span
FROM code_nodes
WHERE repo_id = $1
ORDER BY file_path, line_start;

-- name: ListArchFileEdges :many
-- Returns inter-file edges (excludes self-references) for fan-in/fan-out + edge graph.
--
-- `NOT ce.inferred`: a derived cross-repo API edge names a file in ANOTHER
-- repository, and every metric built on this query — fan-in, fan-out, coupling,
-- choke points — is a statement about THIS repository's architecture. Letting
-- one in adds a foreign file to the file set and inflates the counts.
SELECT src.file_path as source_path, tgt.file_path as target_path, ce.kind
FROM code_edges ce
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
  AND rc.state <> 'suppressed';

