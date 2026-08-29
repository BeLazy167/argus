-- Dropping the column also removes it from the pgGraph projection on the next
-- graph.build(); there is no unregister call to make first.
DROP TRIGGER IF EXISTS code_nodes_fill_installation ON code_nodes;
DROP FUNCTION IF EXISTS code_nodes_fill_installation();
ALTER TABLE code_nodes DROP COLUMN IF EXISTS installation_id;
