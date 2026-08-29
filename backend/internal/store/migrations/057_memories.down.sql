DROP TABLE IF EXISTS memories;
-- The vector extension is deliberately left installed: dropping an extension
-- is a cluster-visible act and other objects may depend on it by the time a
-- rollback runs; re-running 057 up is idempotent either way.
