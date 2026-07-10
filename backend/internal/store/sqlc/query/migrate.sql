-- Backfill queries for cmd/migrate-memory. Each re-derives one memory doc type
-- from Postgres (the source of truth) so the backfill can seed the unified
-- {repo}/_shared Supermemory containers. All are keyset-paginated by the
-- table's primary key ($2 = last-seen id cursor, $3 = page size) so a full
-- sweep is a single forward scan with no OFFSET blowup. Ordering is by id ASC;
-- for UUID PKs (reviews, review_comments) the order is stable-but-not-temporal,
-- which is fine for a complete one-shot sweep.

-- name: ListReviewCommentsForBackfill :many
-- (a) review_comments → type=review docs. Joined up to reviews (pr_number,
-- pr_author, review id) and repos (full_name → container tag). created_at is
-- returned so the caller can apply the --new-shape-since cutoff.
--
-- customID CAVEAT: the review doc customID is backfill-internal ONLY. The live
-- pipeline hashes the RAW finding body, which Postgres does not persist
-- (review_comments.body is the GitHub-formatted body from formatCommentBody), so
-- the backfill hashes the stored body — deterministic + idempotent across
-- re-runs, but NOT collision-identical to a live pipeline write. The
-- --new-shape-since cutoff skips post-deploy rows so this non-identical customID
-- can't duplicate (or, via the batch API's '---' content merge, corrupt) a doc
-- the pipeline already wrote into the new container.
SELECT rc.id, rc.file_path, rc.severity, rc.category, rc.body,
       rv.pr_number, rv.pr_author, rv.id AS review_id, r.full_name, rc.created_at
FROM review_comments rc
JOIN reviews rv ON rv.id = rc.review_id
JOIN repos r ON r.id = rv.repo_id
WHERE r.installation_id = $1 AND rc.id > $2
ORDER BY rc.id ASC
LIMIT $3;

-- name: ListPatternsForBackfill :many
-- (b) patterns → type=pattern docs. LEFT JOIN repos: NULL repo_id is an
-- org-wide/shared pattern (→ _shared); non-NULL routes to {repo}. old_sm_id is
-- the current (legacy) supermemory_id, captured so the caller can remap
-- pattern_stats from the legacy id to the new one before overwriting the column.
SELECT p.id, p.repo_id, p.content, COALESCE(p.source, 'manual') AS source,
       p.category, p.pr_number, p.supermemory_id AS old_sm_id, r.full_name
FROM patterns p
LEFT JOIN repos r ON r.id = p.repo_id
WHERE p.installation_id = $1 AND p.id > $2
ORDER BY p.id ASC
LIMIT $3;

-- name: ListCommentOutcomesForBackfill :many
-- (c) comment_outcomes → type=feedback docs. Only confirmed/dismissed (ignored
-- has no feedback shape). Category must be present — the pipeline only indexes
-- feedback when the comment carries a category. created_at drives the
-- --new-shape-since cutoff.
--
-- content is body-only. The live pipeline's reply-origin feedback (reply.go)
-- appended a "Developer[ explanation]:" reply suffix, but that reply text was
-- NEVER persisted to Postgres: CollectReplyTrace (which would have written a
-- developer_agreed/developer_dismissed decision_trace) has ZERO callers, so no
-- such traces exist to recover it from. Reply text is therefore an SM-only data
-- category; body-only is the accepted re-derivation. The feedback customID
-- hashes the stored comment body + action (reply text is NOT in the customID),
-- so confirmed and dismissed coexist and the customID is exact — the cutoff
-- still excludes post-deploy rows so a differing (suffix-carrying) live doc is
-- never content-merged.
SELECT co.id, co.outcome,
       rc.file_path, rc.category, rc.body,
       rv.pr_number, r.full_name,
       COALESCE(co.created_at, 'epoch'::timestamptz) AS created_at
FROM comment_outcomes co
JOIN review_comments rc ON rc.id = co.review_comment_id
JOIN reviews rv ON rv.id = rc.review_id
JOIN repos r ON r.id = rv.repo_id
WHERE r.installation_id = $1
  AND co.outcome IN ('confirmed', 'dismissed')
  AND rc.category IS NOT NULL
  AND co.id > $2
ORDER BY co.id ASC
LIMIT $3;

-- name: ListScenariosForBackfill :many
-- (d) scenarios → type=scenario docs. Active + repo-scoped only, matching the
-- reconciler: NULL-repo scenarios cannot route to a {repo} container.
SELECT s.id, s.repo_id, s.description, s.severity, s.files, r.full_name
FROM scenarios s
JOIN repos r ON r.id = s.repo_id
WHERE s.installation_id = $1 AND s.repo_id IS NOT NULL AND s.active = TRUE
  AND s.id > $2
ORDER BY s.id ASC
LIMIT $3;

-- name: ListRulesForBackfill :many
-- (f) rules → type=rule docs in _shared. Prod has 0 rules today; implemented
-- for completeness.
SELECT id, category, content, priority
FROM rules
WHERE installation_id = $1 AND id > $2
ORDER BY id ASC
LIMIT $3;

-- name: ListReviewSummariesForBackfill :many
-- (g) reviews (summary present) → type=pr_summary docs. created_at drives the
-- --new-shape-since cutoff. The customID = PRSummaryCustomID(repo, pr_number) is
-- exact, but the content is NOT byte-reproducible: the live pipeline builds it
-- from run.Synthesis + the full reviewed-file list, while the backfill rebuilds
-- it from the reviews columns and approximates `files` from the commented files.
-- Because the batch API merges differing content under the same customId, the
-- cutoff skips post-deploy rows so this approximate content can't corrupt a doc
-- the pipeline already wrote.
SELECT rv.id, rv.pr_number, rv.pr_title, rv.pr_author,
       COALESCE(rv.summary, '') AS summary, rv.score, r.full_name,
       COALESCE((SELECT string_agg(DISTINCT rc.file_path, ', ')
                 FROM review_comments rc WHERE rc.review_id = rv.id), '')::text AS files,
       rv.created_at
FROM reviews rv
JOIN repos r ON r.id = rv.repo_id
WHERE r.installation_id = $1 AND rv.summary IS NOT NULL AND rv.id > $2
ORDER BY rv.id ASC
LIMIT $3;

-- name: RemapPatternStatsSupermemoryID :execrows
-- Repoint a pattern_stats row from its legacy Supermemory id to the new backfill
-- id. pattern_stats.supermemory_id is UNIQUE and previously pointed at the
-- legacy doc; the migration overwrites patterns.supermemory_id, so this keeps
-- the stats row joined to the live doc. Keyed on the legacy id (exact) rather
-- than content_hash — pattern_stats has no writer in the codebase (empty in
-- prod) and patterns has no content_hash column, so the legacy id is the only
-- exact join key.
UPDATE pattern_stats SET supermemory_id = @new_id, updated_at = NOW()
WHERE supermemory_id = @old_id;

-- name: ListRepoFullNamesForInstallation :many
-- Owner/repo full_names for one installation, used by --verify-legacy to build
-- the legacy container tags whose doc counts feed the count-verify gate.
SELECT full_name FROM repos WHERE installation_id = $1 ORDER BY id ASC;
