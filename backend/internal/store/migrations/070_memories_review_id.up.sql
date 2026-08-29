-- Which review wrote this memory.
--
-- Nothing linked the two before. A memory row carried metadata->>'pr_number'
-- and a container_tag of the repo, which identifies the PULL REQUEST, not the
-- review run — and because writes are upserts keyed on
-- (installation_id, custom_id), every re-review of the same PR rewrites the
-- same rows. So the question "what did THIS review learn" had no answer, and
-- the user had no way to tell a working memory path from a dead one. Memory
-- indexing failures are non-fatal and log at Warn, so the failure mode is
-- silence; this column is what makes the write observable.
--
-- One partial linkage did exist and is deliberately NOT what this builds on:
-- docbuild.go stamps metadata->>'review_id' on type=review documents (indexed
-- review comments). It covers exactly one of the eight document types — not the
-- patterns, conventions, file syntheses, PR summary, architecture note, or
-- scenarios the learning stages write — so it can never answer "what did this
-- review learn". Attribution belongs to the WRITER, which is one place, rather
-- than to eight separately-maintained metadata call sites. The two agree where
-- both exist: the same run.ReviewID feeds each.
--
-- Semantics: the review that LAST wrote the row, because the upsert overwrites.
-- A later review that re-learns the same pattern takes ownership of it, and an
-- earlier review's "what I learned" list shrinks accordingly. That is the
-- honest reading of an upserting store — the row genuinely holds the later
-- review's text.
--
-- NULL is the normal state for every row not written by a review run: the
-- phase-0 archive backfill, dashboard-authored rules, and reaction/reply
-- feedback signals. It is also the state of every pre-migration row; there is
-- no backfill, because the information to reconstruct it does not exist.
--
-- ON DELETE SET NULL, not CASCADE: deleting a review must never delete what it
-- taught. The memory outlives the run that produced it — that is the entire
-- point of the store.
ALTER TABLE memories ADD COLUMN IF NOT EXISTS review_id uuid REFERENCES reviews(id) ON DELETE SET NULL;

-- The read is always (installation_id, review_id) — tenant first, exactly as
-- every other memories read is scoped. Partial, because the overwhelming
-- majority of rows are unattributed and indexing them buys nothing. The index
-- also keeps the FK's ON DELETE SET NULL from sequential-scanning memories on
-- every review deletion.
CREATE INDEX IF NOT EXISTS memories_review_idx
  ON memories (installation_id, review_id)
  WHERE review_id IS NOT NULL;
