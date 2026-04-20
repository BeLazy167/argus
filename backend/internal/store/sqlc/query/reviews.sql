-- name: ListReviews :many
SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, COALESCE(head_ref,'') as head_ref, github_review_id,
       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
       deep_review, persona, is_incremental, created_at, completed_at
FROM reviews WHERE repo_id = $1
ORDER BY created_at DESC LIMIT $2 OFFSET $3;

-- name: ListAllReviewsScoped :many
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv
JOIN repos r ON rv.repo_id = r.id
WHERE r.installation_id = ANY($1::bigint[])
ORDER BY rv.created_at DESC LIMIT $2 OFFSET $3;

-- name: GetReview :one
SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, COALESCE(head_ref,'') as head_ref, github_review_id,
       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
       deep_review, persona, is_incremental, created_at, completed_at, simulation_results, diagram, diagram_title
FROM reviews WHERE id = $1;

-- name: GetLastCompletedReview :one
SELECT id, repo_id, pr_number, pr_title, pr_author, head_sha, base_sha, COALESCE(head_ref,'') as head_ref, github_review_id,
       status, summary, score, token_usage, trigger, triggered_by, duration_ms, error,
       deep_review, persona, is_incremental, created_at, completed_at
FROM reviews WHERE repo_id = $1 AND pr_number = $2 AND status = 'completed'
ORDER BY completed_at DESC LIMIT 1;

-- name: GetLatestReviewBySHA :one
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv JOIN repos r ON rv.repo_id = r.id
WHERE r.full_name = $1 AND rv.pr_number = $2 AND rv.head_sha = $3
  AND rv.status = 'completed'
ORDER BY rv.created_at DESC LIMIT 1;

-- name: GetLatestReviewByPR :one
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv JOIN repos r ON rv.repo_id = r.id
WHERE r.full_name = $1 AND rv.pr_number = $2
  AND rv.status = 'completed'
ORDER BY rv.created_at DESC LIMIT 1;

-- name: GetLatestCompletedReviewByPR :one
-- Returns the most recent completed review for a given (repo_id, pr_number)
-- along with its findings payload (AllFileReviews from the latest pipeline
-- state) and the flat set of per-thread auto-resolve keys fired AFTER the
-- review completed. Used by the async cross-PR stage to hydrate linked-PR
-- state in one round trip.
--
-- Findings storage note: findings currently live inside
-- pipeline_states.payload->'AllFileReviews' (see GetAllFileReviewsForReview
-- for the existing pattern). If reviews ever gains a findings_json column,
-- this LEFT JOIN on pipeline_states can collapse.
--
-- Auto-resolve filtering: migration 041 added resolved_thread_keys TEXT[]
-- to auto_resolve_events — each element is a "<path>:<line>" key matching
-- the shape that Finding.Path + Finding.Line produces on the Go side.
-- We flatten the post-review events' arrays via UNNEST + array_agg so the
-- consumer receives a single deduped set ready for O(1) membership tests
-- in filterAutoResolvedFindings. Pre-migration rows contribute an empty
-- array (zero join keys, no filtering happens for them — documented no-op).
--
-- Returns pgx.ErrNoRows when the PR was never reviewed — callers must
-- treat the absence as graceful (Reviewed=false), not as an error.
SELECT
    rv.id,
    rv.repo_id,
    rv.pr_number,
    rv.head_sha,
    rv.completed_at,
    COALESCE(ps.payload->'AllFileReviews', '[]'::jsonb)::jsonb AS all_file_reviews,
    COALESCE(
        (
            SELECT array_agg(DISTINCT key)
            FROM auto_resolve_events are2,
                 UNNEST(are2.resolved_thread_keys) AS key
            WHERE are2.repo_id = rv.repo_id
              AND are2.pr_number = rv.pr_number
              AND are2.created_at > rv.completed_at
              AND key <> ''
        ),
        '{}'
    )::text[] AS auto_resolved_thread_keys
FROM reviews rv
LEFT JOIN LATERAL (
    SELECT payload
    FROM pipeline_states
    WHERE review_id = rv.id
    ORDER BY updated_at DESC
    LIMIT 1
) ps ON TRUE
WHERE rv.repo_id = $1
  AND rv.pr_number = $2
  AND rv.status = 'completed'
ORDER BY rv.completed_at DESC
LIMIT 1;

-- name: CountReviewsThisMonth :one
SELECT COUNT(*)::int FROM reviews r
JOIN repos rp ON r.repo_id = rp.id
WHERE rp.installation_id = $1
AND r.created_at >= date_trunc('month', NOW());

-- name: UpdateReviewStatus :exec
UPDATE reviews SET status = $2, error = $3, completed_at = CASE WHEN $2 IN ('completed','failed','cancelled') THEN NOW() ELSE NULL END
WHERE id = $1;

-- name: ListReviewsScoped :many
SELECT rv.id, rv.repo_id, rv.pr_number, rv.pr_title, rv.pr_author, rv.head_sha, rv.base_sha, COALESCE(rv.head_ref,'') as head_ref, rv.github_review_id,
       rv.status, rv.summary, rv.score, rv.token_usage, rv.trigger, rv.triggered_by, rv.duration_ms, rv.error,
       rv.deep_review, rv.persona, rv.is_incremental, rv.created_at, rv.completed_at
FROM reviews rv
JOIN repos r ON rv.repo_id = r.id
WHERE rv.repo_id = $1 AND r.installation_id = ANY($2::bigint[])
ORDER BY rv.created_at DESC LIMIT $3 OFFSET $4;

-- name: GetRepoReviewStats :one
-- Returns averaged token + cost stats over the last N completed reviews for a repo,
-- used to estimate cost for the "Trigger review" checkbox comment. `cost_available`
-- is true only when at least one review in the sample has token_usage.total.cost.
WITH recent AS (
  SELECT token_usage FROM reviews
  WHERE repo_id = $1 AND status = 'completed' AND token_usage IS NOT NULL
  ORDER BY created_at DESC
  LIMIT $2
)
SELECT
  COUNT(*)::int AS sample_size,
  COALESCE(AVG((token_usage->'total'->>'total_tokens')::bigint), 0)::bigint AS avg_tokens,
  COALESCE(AVG(NULLIF((token_usage->'total'->>'cost')::float8, 0)), 0)::float8 AS avg_cost,
  COALESCE(BOOL_OR((token_usage->'total'->>'cost') IS NOT NULL AND (token_usage->'total'->>'cost')::float8 > 0), false) AS cost_available
FROM recent;

-- name: UpdateReviewCrossPRHash :exec
-- Stores the SHA256 hash of the cross-PR findings+diffs bundle that the async
-- cross-PR stage used to produce its output. Subsequent runs compare against
-- this value and skip the LLM call when unchanged. Persisted so the
-- idempotency check survives machine restarts.
UPDATE reviews SET cross_pr_hash = $2 WHERE id = $1;

-- name: FindReviewsLinkingToPR :many
-- Returns completed reviews whose linked_pr_refs JSONB contains the given
-- (owner, repo, number). Used by OnReviewCompleted to enqueue sibling
-- refreshes when a PR whose review was linked-to finishes a new review.
-- Excludes the caller's own review_id.
--
-- Index: reviews_linked_pr_refs_gin (jsonb_path_ops) — containment (@>) is
-- the GIN-supported operator; any relaxation (e.g. ? or ?&) loses the index.
SELECT rv.id, rv.repo_id, rv.pr_number, rv.head_sha
FROM reviews rv
WHERE rv.status = 'completed'
  AND rv.id <> sqlc.arg(exclude_id)::uuid
  AND rv.linked_pr_refs @> jsonb_build_array(jsonb_build_object(
      'owner',  sqlc.arg(owner)::text,
      'repo',   sqlc.arg(repo)::text,
      'number', sqlc.arg(number)::int
  ));

-- name: SetReviewLinkedPRRefs :exec
-- Writes the JSONB array of linked-PR refs for a review, typically called
-- from the synthesis path BEFORE EventReviewCompleted publish so sibling
-- lookups (FindReviewsLinkingToPR) see up-to-date data.
UPDATE reviews SET linked_pr_refs = $2 WHERE id = $1;

-- name: SetReviewLinkedIssueRefs :exec
-- Writes the JSONB array of linked-issue refs for a review. Same call-site
-- contract as SetReviewLinkedPRRefs: synthesis path, BEFORE
-- EventReviewCompleted publish, so the joint-acceptance stage sees this
-- review's refs the instant a sibling fires.
UPDATE reviews SET linked_issue_refs = $2 WHERE id = $1;

-- name: FindSharedLinkedIssues :many
-- Given a review id, returns the set of (owner, repo, number) issues that
-- appear in THIS review's linked_issue_refs AND are also referenced by at
-- least one other completed review (so count(DISTINCT review_ids) >= 2).
-- Each row carries the aggregated list of review ids pointing at that
-- issue so the joint-acceptance stage can hydrate their findings.
--
-- Correctness notes:
--   - Joining on `r.linked_issue_refs @> jsonb_build_array(t.issue)` uses
--     the GIN(jsonb_path_ops) index from migration 040. Relaxing the
--     operator (e.g. ? / ?&) would lose the index scan.
--   - The target review is allowed to self-match — it counts toward the
--     >=2 threshold precisely because joint acceptance IS about "this PR
--     plus its siblings" (a lone review can't share an issue with itself
--     distinct-id-wise, so the filter still excludes singletons naturally).
--   - status = 'completed' filter is applied to siblings only; the target
--     row's status is not re-verified because the caller is always the
--     just-completed review and race-wise it's safe: a later status flip
--     won't surface stale joint output (the stage re-runs on next event).
SELECT
    (t.issue->>'owner')::text  AS owner,
    (t.issue->>'repo')::text   AS repo,
    (t.issue->>'number')::int  AS number,
    array_agg(DISTINCT r.id)::uuid[] AS review_ids
FROM reviews target
CROSS JOIN LATERAL jsonb_array_elements(target.linked_issue_refs) AS t(issue)
JOIN reviews r
    ON r.status = 'completed'
   AND r.linked_issue_refs @> jsonb_build_array(t.issue)
WHERE target.id = sqlc.arg(review_id)::uuid
GROUP BY 1, 2, 3
HAVING count(DISTINCT r.id) >= 2;

-- name: MergeStageTokenEntry :execrows
-- Merges a StageTokens JSON object into reviews.token_usage under the
-- named scalar bucket (e.g. 'cross_pr', 'acceptance') AND increments
-- the 'total' aggregate in a single atomic UPDATE. Used by async
-- stages whose LLM calls fire AFTER the synthesis-time token_usage
-- commit — the inline orchestrator path doesn't apply to them.
--
-- Parameters:
--   $1 review_id
--   $2 stage_key  — 'cross_pr' | 'acceptance' (or any scalar bucket)
--   $3 entry_json — a single StageTokens JSON object
--
-- Merge semantics (matches RunTokenUsage.addCrossPR / addAcceptance):
--   prompt_tokens/completion_tokens/total_tokens/cost  → summed
--   model/provider                                     → stamped only
--                                                        if currently
--                                                        missing
--
-- Invariants:
--   - If token_usage is NULL it's initialized to '{}'.
--   - If the bucket is missing it's initialized from zero.
--   - If the bucket currently holds a non-object (legacy array / drift)
--     the '->' projection returns NULL and COALESCE treats it as zero —
--     i.e. we rebuild a fresh scalar object rather than error out.
UPDATE reviews
SET token_usage = jsonb_set(
    jsonb_set(
        COALESCE(token_usage, '{}'::jsonb),
        ARRAY[sqlc.arg(stage_key)::text],
        jsonb_build_object(
            'prompt_tokens',
                COALESCE((token_usage -> sqlc.arg(stage_key)::text ->> 'prompt_tokens')::bigint, 0)
                + COALESCE((sqlc.arg(entry)::jsonb ->> 'prompt_tokens')::bigint, 0),
            'completion_tokens',
                COALESCE((token_usage -> sqlc.arg(stage_key)::text ->> 'completion_tokens')::bigint, 0)
                + COALESCE((sqlc.arg(entry)::jsonb ->> 'completion_tokens')::bigint, 0),
            'total_tokens',
                COALESCE((token_usage -> sqlc.arg(stage_key)::text ->> 'total_tokens')::bigint, 0)
                + COALESCE((sqlc.arg(entry)::jsonb ->> 'total_tokens')::bigint, 0),
            'cost',
                COALESCE((token_usage -> sqlc.arg(stage_key)::text ->> 'cost')::float8, 0)
                + COALESCE((sqlc.arg(entry)::jsonb ->> 'cost')::float8, 0),
            'model',
                COALESCE(
                    NULLIF(token_usage -> sqlc.arg(stage_key)::text ->> 'model', ''),
                    NULLIF(sqlc.arg(entry)::jsonb ->> 'model', ''),
                    ''
                ),
            'provider',
                COALESCE(
                    NULLIF(token_usage -> sqlc.arg(stage_key)::text ->> 'provider', ''),
                    NULLIF(sqlc.arg(entry)::jsonb ->> 'provider', ''),
                    ''
                )
        ),
        true
    ),
    '{total}',
    jsonb_build_object(
        'prompt_tokens',
            COALESCE((token_usage -> 'total' ->> 'prompt_tokens')::bigint, 0)
            + COALESCE((sqlc.arg(entry)::jsonb ->> 'prompt_tokens')::bigint, 0),
        'completion_tokens',
            COALESCE((token_usage -> 'total' ->> 'completion_tokens')::bigint, 0)
            + COALESCE((sqlc.arg(entry)::jsonb ->> 'completion_tokens')::bigint, 0),
        'total_tokens',
            COALESCE((token_usage -> 'total' ->> 'total_tokens')::bigint, 0)
            + COALESCE((sqlc.arg(entry)::jsonb ->> 'total_tokens')::bigint, 0),
        'cost',
            COALESCE((token_usage -> 'total' ->> 'cost')::float8, 0)
            + COALESCE((sqlc.arg(entry)::jsonb ->> 'cost')::float8, 0)
    ),
    true
)
WHERE id = sqlc.arg(review_id)::uuid;
