-- When a repo last had its WHOLE tree walked into the code graph.
--
-- Until now only changed files were ever parsed: graph.IndexFiles runs per pull
-- request over that PR's files, and graph.IndexRepo — which walks the entire
-- tree — had no caller at all. The result is a graph made of PR-shaped islands:
-- 13,637 nodes across 4,347 components, 29% of them isolated, because the code
-- each PR connects to was never itself in any diff. A blast-radius query over
-- that returns a fragment and looks like it worked.
--
-- NULL means never fully indexed, which is every existing row, so the backfill
-- picks them up oldest-first without a separate queue table.
--
-- Deliberately NOT a "needs reindex" boolean. A timestamp answers both "has
-- this ever been done" and "how stale is it" with one column, and it cannot
-- get stuck true after a crash the way a flag can.
ALTER TABLE repos ADD COLUMN IF NOT EXISTS graph_indexed_at TIMESTAMPTZ;

-- Partial index on the enabled rows only. The scheduler asks "which enabled
-- repo is most overdue" every tick, and disabled repos are never candidates.
CREATE INDEX IF NOT EXISTS repos_graph_index_due
  ON repos (graph_indexed_at NULLS FIRST)
  WHERE enabled;

-- When a full index was last ATTEMPTED, successful or not.
--
-- Separate from graph_indexed_at because the scheduler needs both answers.
-- Ordering only on graph_indexed_at gives head-of-line blocking: it stays NULL
-- on failure (deliberately, so the repo is retried), the queue takes one repo
-- per tick, and a repo that always fails — archived, transferred, stale default
-- branch, too big for the deadline — is therefore selected forever while every
-- other repo waits behind it. Stamping the attempt moves a failure to the back
-- of the queue without marking it done.
ALTER TABLE repos ADD COLUMN IF NOT EXISTS graph_index_attempted_at TIMESTAMPTZ;

-- Where the next capped full index should resume.
--
-- A capped index takes a deterministic sorted PREFIX of the tree, so without a
-- cursor the same alphabetical tail is excluded on every run forever — on a
-- monorepo where web/ sorts after backend/, an entire top-level tree would never
-- enter the graph while the repo reported as indexed.
--
-- Rotating is safe because the orphan sweep is per-FILE: indexFileSet only
-- removes symbols for files it actually visited, so a run that skips a file
-- leaves that file's existing nodes untouched. Successive windows therefore
-- accumulate coverage instead of fighting each other.
ALTER TABLE repos ADD COLUMN IF NOT EXISTS graph_index_cursor INTEGER NOT NULL DEFAULT 0;
