DROP INDEX IF EXISTS reviews_linked_pr_refs_gin;
ALTER TABLE reviews DROP COLUMN IF EXISTS linked_pr_refs;
