-- Why a review was narrowed, so the dashboard can say it.
--
-- The note lives on PipelineRun, which is persisted to pipeline_states, not to
-- reviews. So a reduced review reached the pull request summary and nothing
-- else: the dashboard showed a completed review with fewer findings over fewer
-- files and no indication that a limit had narrowed it — a quiet review and a
-- deliberately narrowed one looked identical.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS budget_note TEXT;
