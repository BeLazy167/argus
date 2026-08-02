-- Platform embedding default is voyage-4 (native 1024 dims; Matryoshka family).
-- Storage standardizes on 1024: OpenAI BYOK models truncate via their native
-- `dimensions` request param; custom endpoints must serve 1024-dim models (the
-- write path validates vector length before insert). Pre-data, so the column
-- rewrite is instant and the dependent HNSW index rebuilds empty.
ALTER TABLE memories ALTER COLUMN embedding TYPE vector(1024);
