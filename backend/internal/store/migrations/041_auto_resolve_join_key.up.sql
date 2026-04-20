-- Add a per-thread join key to auto_resolve_events so the async cross-PR
-- stage can drop prior-review findings whose thread was already auto-resolved
-- by a later push. Keeping the existing one-row-per-push shape (aggregate
-- stats on resolved_count/attempted_count/github_api_calls continue to work
-- unchanged) — we just attach the flat list of resolved-thread keys per push.
--
-- Each key is a "<file_path>:<line>" string derived from the resolved thread's
-- GitHub location; Finding.Path + Finding.Line produces the same shape on the
-- consumer side. NULL/empty array means "thread-level detail not captured"
-- (pre-migration rows) — filterAutoResolvedFindings treats empty as no-op.
-- resolved_thread_keys is an ARRAY (not a scalar thread_id) because
-- one push can resolve multiple threads atomically. Keeping
-- one-row-per-push preserves the existing aggregate stats queries
-- (resolved_count, attempted_count, github_api_calls) unchanged.
ALTER TABLE auto_resolve_events
    ADD COLUMN resolved_thread_keys TEXT[] NOT NULL DEFAULT '{}';

-- Legacy rows written before the writer was updated retain the zero-length
-- array default; the LEFT JOIN in GetLatestCompletedReviewByPR will simply
-- contribute nothing to the aggregated set for those pushes. This is the
-- documented "no join key available → no filter applied" behaviour.
