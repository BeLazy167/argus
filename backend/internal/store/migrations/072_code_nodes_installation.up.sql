-- Denormalise the tenant boundary onto code_nodes.
--
-- Blast radius traversal was scoped by repo_id (#220). That is a strictly
-- SMALLER boundary than the tenant, and it blocks the cross-repo dependency
-- edges #221 exists to find: an API repo and a web repo inside the same
-- installation legitimately link, and a repo-scoped walk can never cross that
-- link. Widening to "no boundary" is not an option -- code_edges is registered
-- with pgGraph without a repo predicate, so an unfiltered walk can reach ANOTHER
-- CUSTOMER'S nodes. installation_id is the boundary that is both wide enough for
-- cross-repo and tight enough for tenancy, and it is the same boundary every
-- memory query already uses.
--
-- It has to be a COLUMN ON code_nodes rather than a join to repos, because
-- pgGraph filters are evaluated inside the traversal against registered columns
-- of the node table. A join cannot be pushed into graph.traverse(), and applying
-- the tenant predicate to traverse's OUTPUT is not equivalent: max_rows is
-- enforced inside the walk, so foreign-tenant nodes would consume the row budget
-- and then be discarded, returning fewer rows than the CTE or none at all.
ALTER TABLE code_nodes ADD COLUMN IF NOT EXISTS installation_id BIGINT;

-- Backfill BEFORE the NOT NULL. A NULL here is not a cosmetic gap: every read
-- path filters on installation_id, so a NULL row is invisible to every
-- traversal -- the node silently disappears from blast radius instead of
-- erroring. code_nodes.repo_id is NOT NULL and FK-cascades off repos, so this
-- join covers every existing row.
UPDATE code_nodes cn
SET installation_id = r.installation_id
FROM repos r
WHERE r.id = cn.repo_id
  AND cn.installation_id IS DISTINCT FROM r.installation_id;

-- Fill the column for writers that do not know about it. This exists for one
-- specific failure: this migration is the Fly release_command, so it COMPLETES
-- before the rolling update starts, and for the 1-2 minutes both machines take
-- to roll, the PREVIOUS binary is still serving. Its UpsertCodeNode omits
-- installation_id, so without this trigger every code_nodes INSERT in that
-- window hits the NOT NULL below and is rejected: the indexer logs
-- "graph: upsert node failed", skips the symbol, and then still runs its orphan
-- sweep, leaving a partially emptied graph for the files it touched. Migration
-- 069 dodged this by giving its NOT NULL column a DEFAULT; no default can
-- derive a value from another column, so the derivation has to be a trigger.
--
-- Deliberately narrow: it fires only when the value is NULL, so it never
-- overwrites what the current binary derived and never masks a writer that
-- stamps the WRONG tenant.
CREATE OR REPLACE FUNCTION code_nodes_fill_installation() RETURNS trigger AS $fn$
BEGIN
    IF NEW.installation_id IS NULL THEN
        SELECT r.installation_id INTO NEW.installation_id FROM repos r WHERE r.id = NEW.repo_id;
    END IF;
    RETURN NEW;
END
$fn$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS code_nodes_fill_installation ON code_nodes;
CREATE TRIGGER code_nodes_fill_installation
    BEFORE INSERT OR UPDATE ON code_nodes
    FOR EACH ROW EXECUTE FUNCTION code_nodes_fill_installation();

ALTER TABLE code_nodes ALTER COLUMN installation_id SET NOT NULL;

-- NO index on installation_id, and that is a decision rather than an omission.
-- Neither query reaches code_nodes through one. The seed lookup filters
-- (installation_id, repo_id, file_path) and the planner takes the far more
-- selective idx_code_nodes_file (repo_id, file_path) from migration 014,
-- applying installation_id as a recheck. The recursive step arrives by primary
-- key through code_edges.source_id, so installation_id is again a post-fetch
-- filter. The pgGraph path evaluates its own FilterIndex, not a btree. A
-- single-column btree on a value that is one-per-tenant would therefore be
-- maintained on every one of the tens of thousands of node upserts a whole-repo
-- index pass performs, and serve zero reads.

-- Register installation_id with pgGraph so graph.eq() can evaluate it INSIDE
-- graph.traverse(), then rebuild so the FilterIndex actually carries the column.
--
-- Guarded and best-effort on purpose, and both halves of that matter:
--   * pgGraph is an optional extension. It needs a custom build and is absent
--     from every managed Postgres and every self-hosted install, where the
--     recursive CTE is the only path and is correct. This migration runs as the
--     Fly release_command, so raising here would fail the deploy over an
--     extension the deployment does not have.
--   * If registration is skipped or fails, graph.eq('installation_id', ...)
--     errors at query time, blastRadiusPGGraph returns an error, and
--     GetBlastRadius falls back to the CTE -- which carries the same predicate.
--     The boundary therefore holds either way; only the fast path is lost.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'graph') THEN
        -- 'numeric' because pgGraph filter columns are typed from its own
        -- supported set (numeric/text/boolean/date/timestamptz/uuid); a bigint
        -- column is registered as numeric and matched against a JSON number.
        PERFORM graph.add_filter_column('public.code_nodes'::regclass, 'installation_id', 'numeric');
        PERFORM graph.build();
    END IF;
EXCEPTION WHEN OTHERS THEN
    RAISE NOTICE 'pgGraph installation_id filter not registered (%); blast radius will use the recursive CTE', SQLERRM;
END
$$;
