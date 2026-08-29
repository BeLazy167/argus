-- Safe to run where the extension was never installed. Where it WAS installed
-- and the ownership conversion has run, memories.embedding is a
-- pgcontext.vector and RESTRICT (the default) makes Postgres refuse rather
-- than cascade -- which is what we want. A failure here means "run the
-- conversion rollback first", not "delete 3,896 embeddings".
DROP EXTENSION IF EXISTS pgcontext;
