-- Migration 040: adds reviews.linked_issue_refs JSONB for the joint
-- acceptance stage. Populated at synthesis time alongside
-- linked_pr_refs (see crosspr_stage.persistReviewLinkedIssueRefs).
--
-- Mirrors migration 039's pattern for linked_pr_refs: a JSONB array of
-- {owner, repo, number} plus a GIN index using jsonb_path_ops so the
-- containment (@>) lookup in FindSharedLinkedIssues stays indexed.
--
-- Populated shape (keep in sync with persistReviewLinkedIssueRefs):
--   [{"owner":"acme","repo":"api","number":42}, ...]
--
-- Default '[]'::jsonb so the NOT NULL constraint never fires on old rows
-- during the backfill-free deployment (existing rows get the default).
ALTER TABLE reviews
    ADD COLUMN linked_issue_refs JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX reviews_linked_issue_refs_gin
    ON reviews USING GIN (linked_issue_refs jsonb_path_ops);
