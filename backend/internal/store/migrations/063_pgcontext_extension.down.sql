-- Reversing this is only safe while memories.embedding is still a public.vector.
-- Once the ownership conversion has run, the column is a pgcontext.vector and
-- DROP EXTENSION would take the column with it. RESTRICT (the default) makes
-- Postgres refuse rather than cascade, which is the behaviour we want: a
-- failure here means "run the conversion rollback first", not "delete 3,896
-- embeddings".
DROP EXTENSION IF EXISTS pgcontext;
