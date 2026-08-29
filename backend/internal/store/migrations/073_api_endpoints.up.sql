-- Cross-repo API edges: the route table, the call sites, and the derived edge.
--
-- An API repo declares GET /api/v1/jobs/{id}; a web repo calls
-- fetch(`/api/v1/jobs/${id}`). The two sides share no AST reference and no
-- symbol identity, so no parser and no SCIP-style symbol index can ever produce
-- that edge. Blast radius therefore stops at the repository boundary and
-- reports a change to a handler as affecting nothing outside its own repo.
--
-- api_endpoints materialises the two halves as ordinary rows. Matching is plain
-- string equality over (method, normalised path) inside ONE installation, so
-- the derived edge lands in code_edges and traversal needs no special case —
-- the same shape Glean uses for derived predicates.
CREATE TABLE IF NOT EXISTS api_endpoints (
    id           BIGSERIAL PRIMARY KEY,
    repo_id      BIGINT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    -- The symbol this endpoint belongs to: the handler for a route, the calling
    -- function for a call site. NOT NULL because an endpoint we cannot place in
    -- the graph cannot become an edge, and a row that can never become an edge
    -- is a row that lies about coverage.
    node_id      BIGINT NOT NULL REFERENCES code_nodes(id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('server','client')),
    -- 'ANY' for a registration that names no verb (mux.HandleFunc, Flask
    -- @route without methods=). It matches every client method because the
    -- handler really does serve them all.
    method       TEXT NOT NULL,
    -- Normalised: every parameter syntax collapses to '{}' and every catch-all
    -- to '*', so '/jobs/:id', '/jobs/{id}' and '/jobs/<int:id>' compare equal.
    path_pattern TEXT NOT NULL,
    -- As written. Provenance: a UI showing an inferred edge must be able to
    -- show the two strings that justified it, not just the normalised form.
    raw_path     TEXT NOT NULL,
    file_path    TEXT NOT NULL,
    line         INT NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One row per registration site. Line is part of the key because a file may
-- call the same endpoint from two functions, and those are two different edges.
CREATE UNIQUE INDEX IF NOT EXISTS api_endpoints_unique
    ON api_endpoints (repo_id, role, method, path_pattern, file_path, line);

-- The indexer rewrites a file's endpoints wholesale after re-parsing it.
CREATE INDEX IF NOT EXISTS api_endpoints_repo_file
    ON api_endpoints (repo_id, file_path);

-- No installation_id column here on purpose. It would have to be kept in sync
-- with repos.installation_id, and a denormalised tenant key that drifts is a
-- cross-tenant leak. The matcher joins through repos instead, so the boundary
-- has exactly one definition.

-- 'calls_api' is the distinct kind that tells an inferred edge from a parsed
-- one. It is the MINIMUM signal, not the whole answer — see the inferred column
-- below.
ALTER TABLE code_edges DROP CONSTRAINT IF EXISTS code_edges_kind_check;
ALTER TABLE code_edges ADD CONSTRAINT code_edges_kind_check
    CHECK (kind IN ('calls','imports','inherits','implements','uses_type','calls_api'));

-- Whether this edge was DERIVED rather than parsed.
--
-- The risk this column exists to close: an inferred edge sits in the same table
-- as a parsed one, and any consumer that treats both as fact will silently
-- claim an unrelated repository is affected by a change. The distinct kind
-- alone forces every such consumer to enumerate kinds, and that list grows —
-- the next derived relation would need every query updated again, and the ones
-- that were missed would keep reading derived rows as fact.
--
-- One boolean is the durable form of the question "is this a fact or an
-- inference", so `WHERE NOT inferred` stays correct as kinds are added.
--
-- KNOWN GAP, stated so nobody reads the column as more than it is: the LLM
-- architecture-graph pass (orchestrator.go extractArchitectureGraph) also
-- writes edges it derived rather than parsed, and it still writes them with
-- inferred = false. Marking that path is a behaviour change to a different
-- feature with no test seam, so it is deliberately not done here. Today the
-- column means exactly "written by the cross-repo API matcher", and it must be
-- extended to the LLM path before anything treats `NOT inferred` as "parsed".
ALTER TABLE code_edges ADD COLUMN IF NOT EXISTS inferred BOOLEAN NOT NULL DEFAULT false;
