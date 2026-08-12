-- Preserve retry and omission facts after staged payloads are removed at publish.
ALTER TABLE graph_index_generations
    ADD COLUMN unavailable_files INTEGER NOT NULL DEFAULT 0 CHECK (unavailable_files >= 0);
ALTER TABLE graph_index_generation_files
    ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0);

UPDATE graph_index_generations g
SET unavailable_files = counted.unavailable
FROM (
    SELECT generation_id, count(*) FILTER (WHERE status = 'unavailable')::integer AS unavailable
    FROM graph_index_generation_files
    GROUP BY generation_id
) counted
WHERE g.id = counted.generation_id;
