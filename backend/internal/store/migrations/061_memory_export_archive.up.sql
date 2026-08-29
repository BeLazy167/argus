-- Phase-0 safety net for the memory-in-Postgres migration
-- (docs/memory-pg/02-migration-plan.md §2.2). Re-derivation rebuilds ~90% of
-- the corpus from Postgres, but four classes exist ONLY in Supermemory —
-- synthesis file memories, reply_feedback learnings, dismissal `reason` extras,
-- and `_shared` decayed confidence values. Those are unrecoverable once the
-- containers are gone, so a raw one-shot export is taken BEFORE anything else
-- and the SM-only classes are imported from here.
--
-- Deliberately dumb storage: the whole document lands as jsonb, unparsed. The
-- import step decides what to do with it later, and a schema that tried to
-- model Supermemory's shapes now would have to be migrated the moment one of
-- them surprised us. This table is a tape backup, not a model.
CREATE TABLE memory_export_archive (
  id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  installation_id bigint NOT NULL REFERENCES installations(id),
  container_tag   text   NOT NULL,
  -- The Supermemory server id. Uniqueness rides on this rather than custom_id:
  -- customIds may be empty on legacy docs and can COLLIDE across
  -- merge-corrupted rows (the batch API merges content on customId collision —
  -- the quirk the whole `--new-shape-since` defense existed for). Keying on a
  -- colliding value would silently drop exactly the damaged documents this
  -- archive exists to preserve evidence of.
  doc_id          text   NOT NULL,
  custom_id       text,
  payload         jsonb  NOT NULL,
  exported_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT memory_export_doc_uniq UNIQUE (installation_id, doc_id)
);

-- The import step reads by installation + container (synthesis and
-- reply_feedback live in different containers), and filters on the document's
-- own type.
CREATE INDEX memory_export_scope_idx ON memory_export_archive (installation_id, container_tag);
CREATE INDEX memory_export_type_idx ON memory_export_archive ((payload->'metadata'->>'type'));
