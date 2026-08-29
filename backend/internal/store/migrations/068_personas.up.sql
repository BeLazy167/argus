-- Personas as rows, not as a Go enum with prompt text inline in a switch.
--
-- Adding a ninth persona used to mean a new constant, a ValidPersonas entry and
-- two switch cases — a code change and a deploy. And there was exactly ONE
-- custom slot: settings carried a single custom_persona_prompt string, so
-- writing a second custom persona overwrote the first, and neither had a name,
-- so every one of them displayed as the literal word "custom".
--
-- NOT stored in prompt_templates, which was the obvious guess. That table is
-- UNIQUE(repo_id, stage) and REPLACES a stage's system prompt. A persona is an
-- overlay APPENDED to the prompt, carries a second string for the specialist
-- path, and is selected by name from a set. Reusing that table would have meant
-- adding a name, adding a kind, and dropping the unique constraint — a
-- different table wearing the old one's name.
--
-- installation_id is nullable: NULL marks the built-ins, which every
-- installation shares and nobody owns.
CREATE TABLE IF NOT EXISTS personas (
  id              SERIAL PRIMARY KEY,
  installation_id BIGINT REFERENCES installations(id) ON DELETE CASCADE,
  slug            TEXT NOT NULL,
  name            TEXT NOT NULL,
  prompt_overlay  TEXT NOT NULL,
  specialist_hint TEXT NOT NULL DEFAULT '',
  is_builtin      BOOLEAN NOT NULL DEFAULT FALSE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One slug per installation, and one global slug per built-in. Two partial
-- indexes rather than one constraint, because NULL never equals NULL in a
-- unique index and the built-ins would otherwise be duplicable without limit.
CREATE UNIQUE INDEX IF NOT EXISTS personas_install_slug
  ON personas (installation_id, slug) WHERE installation_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS personas_builtin_slug
  ON personas (slug) WHERE installation_id IS NULL;
