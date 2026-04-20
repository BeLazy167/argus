DROP INDEX IF EXISTS reviews_linked_issue_refs_gin;
ALTER TABLE reviews DROP COLUMN IF EXISTS linked_issue_refs;
