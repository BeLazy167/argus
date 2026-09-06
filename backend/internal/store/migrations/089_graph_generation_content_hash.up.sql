-- A full graph publish deletes every code_node for the repo and re-inserts the
-- staged generation. That swap is the visibility boundary, so it cannot be made
-- incremental -- but when the staged generation reproduces the live graph
-- exactly, the swap rewrites every row for no change at all.
--
-- Between 2026-08-12 and 2026-09-03 that churn wrote 10.2M rows into the
-- pggraph CDC log (graph._sync_log, 5.9GB) and filled the database volume.
--
-- content_hash fingerprints a generation's staged file payloads so publish can
-- recognise a no-op and skip the swap. Empty means "not yet computed", which
-- always publishes.
ALTER TABLE graph_index_generations ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';

-- Matching hashes prove the staged INPUT is unchanged. They cannot prove the
-- live graph still matches it: the PR indexer deletes orphaned nodes between
-- full publishes (DeleteNodesByIDs), and a deletion moves no updated_at, so a
-- "nothing written since publish" test passes over a graph that has silently
-- lost rows. Recording what the publish actually left behind turns that into a
-- positive check.
--
-- Both counts exclude inferred cross-repo `calls_api` edges. Those are not part
-- of the staged generation: LinkAPIEndpoints rewrites them installation-wide
-- after every publish commits, so counting them would compare against rows that
-- are replaced moments later.
--
-- NULL means "not recorded", which always publishes — same fail-open reading as
-- an empty content_hash.
ALTER TABLE graph_index_generations ADD COLUMN published_node_count BIGINT;
ALTER TABLE graph_index_generations ADD COLUMN published_edge_count BIGINT;
