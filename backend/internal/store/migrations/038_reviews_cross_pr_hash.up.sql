-- Enables idempotency for the async cross-PR stage: the stage stores
-- hash(linked_pr_findings_bundle) here after each run. A subsequent
-- refresh with the same hash is a no-op. Survives machine restarts;
-- cheaper and more durable than an in-memory LRU.
ALTER TABLE reviews ADD COLUMN cross_pr_hash VARCHAR(64);
