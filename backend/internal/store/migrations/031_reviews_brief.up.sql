-- Add brief column to reviews table so the LLM-generated conversational
-- verdict from synthesize() can be surfaced on the dashboard. Previously
-- run.Synthesis.Brief only went to the GitHub PR comment body; the dashboard
-- showed run.Synthesis.Summary (the raw file-by-file dump) in the Summary
-- card, which duplicated the per-file comment sections below it.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS brief TEXT;
