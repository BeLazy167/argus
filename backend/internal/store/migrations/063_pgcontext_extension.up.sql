-- Install pgContext IF THE SERVER HAS IT. Optional by design.
--
-- The first version of this migration ran a bare CREATE EXTENSION and hard
-- failed everywhere the extension is not installed:
--   migrate up failed: extension "pgcontext" is not available (0A000)
--
-- That is not a CI inconvenience. pgContext needs a custom index access method
-- and cannot be installed on ANY managed Postgres -- RDS, Aurora, Supabase,
-- Cloud SQL, Neon. A hard requirement here bricks every self-hosted install of
-- an open-source, self-deployable product. pgvector is in every default
-- catalog; pgContext is in none.
--
-- So: best effort. Where pgContext exists (our production image) the column is
-- converted by the operator step and retrieval uses pgcontext operators. Where
-- it does not, everything stays on pgvector and works exactly as before. The
-- reader probes the actual column type at runtime rather than assuming either.
--
-- This migration still installs NOTHING beyond the extension. The ownership
-- conversion takes explicit safety gates (application_dependencies_reviewed,
-- sessions_drained) that a rolling release_command cannot honestly assert.
DO $$
BEGIN
  CREATE EXTENSION IF NOT EXISTS pgcontext;
EXCEPTION
  WHEN undefined_file OR feature_not_supported OR insufficient_privilege THEN
    RAISE NOTICE 'pgcontext not available; continuing on pgvector (expected on managed Postgres and CI)';
END
$$;
